package modeltrace

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// MinSamplesPerModel 每模型最少样本数（低于此值建库直接报错，防止小样本
// 指纹库产生虚假置信）。
const MinSamplesPerModel = 8

// defaultBeta 校准失败/缺档时的 softmax 温度兜底。
const defaultBeta = 3.0

// Sample 一次成功挑战采集的原始样本。
type Sample struct {
	Model         string
	AccountID     int64
	ExpectedCount int
	Numbers       []int
	LatencyMS     int
}

// ModelProfile 单模型指纹。
type ModelProfile struct {
	Model           string    `json:"model"`
	Samples         int       `json:"samples"`
	Marginal        []float64 `json:"marginal"`         // 355 维平均计数质心
	OrderedCentroid []float64 `json:"ordered_centroid"` // 74 维质心
	OrderedVariance []float64 `json:"ordered_variance"` // 74 维总体方差
}

// Bank 指纹库。
type Bank struct {
	Version       int             `json:"version"`
	Alpha         float64         `json:"alpha"`
	OrderedWeight float64         `json:"ordered_weight"`
	Calibration   map[int]float64 `json:"calibration"` // 有效回答数(封顶3)→softmax β
	Profiles      []*ModelProfile `json:"profiles"`
}

// Answer 一次检测探测的有效载荷。
type Answer struct {
	ExpectedCount int
	Numbers       []int
}

// ModelScore 单模型得分明细（供结果展示/排查）。
type ModelScore struct {
	Model       string  `json:"model"`
	HistScore   float64 `json:"hist_score"`  // −hellinger（越大越近）
	OrderScore  float64 `json:"order_score"` // −标准化欧氏（越大越近）
	Combined    float64 `json:"combined"`    // 多回答平均后的 z 融合分
	Probability float64 `json:"probability"`
}

// Verdict 判定结果。
type Verdict struct {
	Model       string       `json:"model"` // 判定实际服务的模型；无有效回答为空
	Probability float64      `json:"probability"`
	TopHits     int          `json:"top_hits"` // 单回答 argmax 与最终判定一致的回答数
	ValidRuns   int          `json:"valid_runs"`
	Reasons     []string     `json:"reasons,omitempty"`
	PerModel    []ModelScore `json:"per_model"`
}

// BuildBank 从样本构建指纹库：每模型质心/方差 + 留一（LOO）交叉校准 β。
// 任一模型样本数 < MinSamplesPerModel 时返回错误（列出不足的模型）。
func BuildBank(samples []Sample) (*Bank, error) {
	byModel := map[string][]Sample{}
	for _, s := range samples {
		if s.Model == "" || len(s.Numbers) == 0 {
			continue
		}
		byModel[s.Model] = append(byModel[s.Model], s)
	}
	if len(byModel) < 2 {
		return nil, fmt.Errorf("指纹库至少需要 2 个模型的样本，当前 %d 个", len(byModel))
	}
	var insufficient []string
	for m, ss := range byModel {
		if len(ss) < MinSamplesPerModel {
			insufficient = append(insufficient, fmt.Sprintf("%s(%d)", m, len(ss)))
		}
	}
	if len(insufficient) != 0 {
		sort.Strings(insufficient)
		return nil, fmt.Errorf("样本不足（最少 %d 个/模型）：%s", MinSamplesPerModel, joinStrings(insufficient, ", "))
	}
	profiles := make([]*ModelProfile, 0, len(byModel))
	for m, ss := range byModel {
		profiles = append(profiles, buildProfile(m, ss))
	}
	sortProfiles(profiles)
	bank := &Bank{
		Version:       1,
		Alpha:         Alpha,
		OrderedWeight: OrderedWeight,
		Calibration:   map[int]float64{},
		Profiles:      profiles,
	}
	calibrateBank(bank, byModel)
	return bank, nil
}

// buildProfile 单模型质心/方差。
func buildProfile(model string, ss []Sample) *ModelProfile {
	p := &ModelProfile{
		Model:           model,
		Samples:         len(ss),
		Marginal:        make([]float64, MaxValue),
		OrderedCentroid: make([]float64, OrderedDims),
		OrderedVariance: make([]float64, OrderedDims),
	}
	feats := make([][]float64, len(ss))
	for i, s := range ss {
		hist := NumberHistogram(s.Numbers)
		for j, c := range hist {
			p.Marginal[j] += c
		}
		feats[i] = OrderedBlockFeature(s.Numbers)
		for j, v := range feats[i] {
			p.OrderedCentroid[j] += v
		}
	}
	n := float64(len(ss))
	for j := range p.Marginal {
		p.Marginal[j] /= n
	}
	for j := range p.OrderedCentroid {
		p.OrderedCentroid[j] /= n
	}
	for _, f := range feats {
		for j, v := range f {
			d := v - p.OrderedCentroid[j]
			p.OrderedVariance[j] += d * d
		}
	}
	for j := range p.OrderedVariance {
		p.OrderedVariance[j] /= n
	}
	return p
}

func sortProfiles(profiles []*ModelProfile) {
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Model < profiles[j].Model })
}

func joinStrings(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}

// calibrateBank 留一（LOO）交叉校准：对组大小 g∈{1,2,3}，把每模型样本切成
// 大小 g 的组，组外样本重建 LOO 库对组内回答打分，在 β 网格上选多类 Brier
// 误差最小的 β。Analyze 用 min(有效回答数,3) 档位的 β。
func calibrateBank(bank *Bank, byModel map[string][]Sample) {
	for g := 1; g <= 3; g++ {
		type evalCase struct {
			combined []float64
			trueIdx  int
		}
		var evals []evalCase
		for m, ss := range byModel {
			if len(ss) < MinSamplesPerModel+g {
				continue
			}
			for start := 0; start+g <= len(ss); start += g {
				held := ss[start : start+g]
				rest := make([]Sample, 0, len(ss)-g)
				rest = append(rest, ss[:start]...)
				rest = append(rest, ss[start+g:]...)
				loo := make(map[string][]Sample, len(byModel))
				for mm, sss := range byModel {
					if mm == m {
						loo[mm] = rest
					} else {
						loo[mm] = sss
					}
				}
				lp := make([]*ModelProfile, 0, len(loo))
				for mm, sss := range loo {
					lp = append(lp, buildProfile(mm, sss))
				}
				sortProfiles(lp)
				trueIdx := -1
				for i, p := range lp {
					if p.Model == m {
						trueIdx = i
						break
					}
				}
				if trueIdx < 0 {
					continue
				}
				answers := make([]Answer, 0, g)
				for _, s := range held {
					answers = append(answers, Answer{ExpectedCount: s.ExpectedCount, Numbers: s.Numbers})
				}
				combined, ok := combinedAgainst(lp, answers)
				if !ok {
					continue
				}
				evals = append(evals, evalCase{combined: combined, trueIdx: trueIdx})
			}
		}
		if len(evals) == 0 {
			bank.Calibration[g] = defaultBeta
			continue
		}
		bestBeta, bestBrier := defaultBeta, math.Inf(1)
		for steps := 1; steps <= 24; steps++ {
			beta := 0.5 * float64(steps)
			brier := 0.0
			for _, e := range evals {
				probs := softmax(beta, e.combined)
				for i, p := range probs {
					target := 0.0
					if i == e.trueIdx {
						target = 1.0
					}
					brier += (target - p) * (target - p)
				}
			}
			if brier < bestBrier {
				bestBeta, bestBrier = beta, brier
			}
		}
		bank.Calibration[g] = bestBeta
	}
}

// combinedAgainst 打分核心：多回答对给定 profiles 的融合分（跨模型 z 归一，
// (1−w)·hist + w·ordered，再对回答取平均）。ok=false 表示无有效打分。
func combinedAgainst(profiles []*ModelProfile, answers []Answer) ([]float64, bool) {
	if len(profiles) == 0 || len(answers) == 0 {
		return nil, false
	}
	w := OrderedWeight
	if w < 0 {
		w = 0
	}
	if w > 1 {
		w = 1
	}
	combined := make([]float64, len(profiles))
	for _, a := range answers {
		hist := NumberHistogram(a.Numbers)
		feat := OrderedBlockFeature(a.Numbers)
		histScores := make([]float64, len(profiles))
		orderScores := make([]float64, len(profiles))
		for i, p := range profiles {
			histScores[i] = -HellingerDistance(hist, p.Marginal)
			orderScores[i] = -standardizedEuclid(feat, p.OrderedCentroid, p.OrderedVariance)
		}
		zh := zNormalize(histScores)
		zo := zNormalize(orderScores)
		for i := range profiles {
			combined[i] += (1-w)*zh[i] + w*zo[i]
		}
	}
	for i := range combined {
		combined[i] /= float64(len(answers))
	}
	return combined, true
}

// perAnswerCombined 单回答融合分（TopHits 统计用）。
func perAnswerCombined(profiles []*ModelProfile, a Answer) []float64 {
	combined, ok := combinedAgainst(profiles, []Answer{a})
	if !ok {
		return nil
	}
	return combined
}

// argmax 最大值下标（并列取第一个）。
func argmax(v []float64) int {
	if len(v) == 0 {
		return -1
	}
	best := 0
	for i := 1; i < len(v); i++ {
		if v[i] > v[best] {
			best = i
		}
	}
	return best
}

// Analyze 判定：先过滤有效回答（数字个数 ≥ 阈值），再融合打分 + softmax。
// 无有效回答时返回 Verdict{Model:""}（ValidRuns=0，Reasons 带原因）。
func (b *Bank) Analyze(answers []Answer) (*Verdict, error) {
	if b == nil {
		return nil, fmt.Errorf("bank 未加载")
	}
	if len(b.Profiles) < 2 {
		return nil, fmt.Errorf("指纹库模型数不足（%d）", len(b.Profiles))
	}
	verdict := &Verdict{}
	for i, a := range answers {
		threshold := ValidAnswerThreshold(a.ExpectedCount)
		if len(a.Numbers) < threshold {
			verdict.Reasons = append(verdict.Reasons,
				fmt.Sprintf("回答 %d 数字不足（%d < 阈值 %d）", i+1, len(a.Numbers), threshold))
			continue
		}
		verdict.ValidRuns++
	}
	if verdict.ValidRuns == 0 {
		verdict.Reasons = append(verdict.Reasons, "无有效回答，无法判定")
		return verdict, nil
	}
	valid := make([]Answer, 0, len(answers))
	for _, a := range answers {
		if len(a.Numbers) >= ValidAnswerThreshold(a.ExpectedCount) {
			valid = append(valid, a)
		}
	}
	combined, ok := combinedAgainst(b.Profiles, valid)
	if !ok {
		return nil, fmt.Errorf("打分失败")
	}
	beta := defaultBeta
	if bts, exists := b.Calibration[minInt(len(valid), 3)]; exists && bts > 0 {
		beta = bts
	}
	probs := softmax(beta, combined)
	winner := argmax(combined)
	verdict.Model = b.Profiles[winner].Model
	verdict.Probability = probs[winner]
	for _, a := range valid {
		if pa := perAnswerCombined(b.Profiles, a); pa != nil && argmax(pa) == winner {
			verdict.TopHits++
		}
	}
	// 明细：取首个有效回答的原始距离（展示用），融合分/概率为最终值。
	hist := NumberHistogram(valid[0].Numbers)
	feat := OrderedBlockFeature(valid[0].Numbers)
	for i, p := range b.Profiles {
		verdict.PerModel = append(verdict.PerModel, ModelScore{
			Model:       p.Model,
			HistScore:   -HellingerDistance(hist, p.Marginal),
			OrderScore:  -standardizedEuclid(feat, p.OrderedCentroid, p.OrderedVariance),
			Combined:    combined[i],
			Probability: probs[i],
		})
	}
	return verdict, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MarshalBank 序列化指纹库。
func MarshalBank(b *Bank) ([]byte, error) { return json.Marshal(b) }

// ParseBank 反序列化指纹库。
func ParseBank(data []byte) (*Bank, error) {
	var b Bank
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse bank: %w", err)
	}
	if len(b.Profiles) == 0 {
		return nil, fmt.Errorf("bank 无模型档案")
	}
	if b.Calibration == nil {
		b.Calibration = map[int]float64{}
	}
	if b.OrderedWeight <= 0 || b.OrderedWeight > 1 {
		b.OrderedWeight = OrderedWeight
	}
	return &b, nil
}
