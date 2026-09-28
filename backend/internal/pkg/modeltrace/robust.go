package modeltrace

// 稳健数字指纹库（robust bank）：上游 ModelTrace 项目的统一全局指纹库 +
// 配套评分引擎的第二实现。与 bank.go 的自建库并存：
//   - 自建库（bank.go）：自有官号采样、LOO 校准；样本不足时区分度差
//     （7 模型 9-15 样本下 β 全落最低档）。
//   - 稳健库（本文件）：16 模型 × 36 响应 × 12 采集环境，扰动投影
//     （nuisance basis）+ 环境最大池化 + 分组交叉验证温度校准，CV 准确率
//     1/2/3 查询档分别为 95.3%/99.7%/100%。
//
// 移植来源（MIT，Copyright (c) 2026 xqy2006，见 robust_bank_LICENSE 与
// robust_bank_SOURCE.md）：
//   - 指纹库 robust_bank.json = xqy2006/ModelTrace static/data/unified_bank.json
//     （经 MACOS-DO/sub4api 分发，两份拷贝逐字节一致）；
//   - 评分引擎 = ModelTrace static/fingerprint-core.mjs（参考 sub4api 的 Go
//     移植 internal/service/codex_fingerprint.go，算法逐句对齐）；
//   - 挑战生成器 GenerateRobustChallenges = static/challenge-browser.js
//     （模板语料与其建库环境同源——稳健库的顺序块环境质心按这 12 个采集
//     环境池化，检测/采集用同一生成器才能对齐环境分布）。
//
// 评分管线（与上游一致）：
//  1. 边际（355 维）：计数直方图 → Hellinger 特征 sqrt((c+α)/(N+αK)) → 全局
//     标准化（feature_mean/scale）→ 减扰动基投影 → 单位化 → 与质心点积 →
//     跨模型 z 标准化；
//  2. 顺序块（74 维）：4 段×16 值桶 + 10 个尾数桶，均带 0.5 先验 → 标准化 →
//     单位化 → 环境质心得分取 max（模板分量）与减扰后质心得分（nuisance
//     分量）各 0.5 融合 → z 标准化；
//  3. 融合：(1−0.25)·边际 + 0.25·顺序块；多回答取均值；
//  4. softmax 温度取校准表 min(有效回答数,3) 档的 β。

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"sync"
)

//go:embed robust_bank.json
var robustBankJSON []byte

// robustBankFile robust_bank.json 的原始结构。
type robustBankFile struct {
	Schema           string                       `json:"schema"`
	BuiltAt          string                       `json:"built_at"`
	RecommendedQuery int                          `json:"recommended_queries"`
	Models           []robustBankModelFile        `json:"models"`
	Robust           robustArtifactsFile          `json:"robust"`
	Calibration      map[string]robustCalibration `json:"calibration"`
}

type robustBankModelFile struct {
	ID            string         `json:"id"`
	DisplayName   string         `json:"display_name"`
	Family        string         `json:"family"`
	FamilyName    string         `json:"family_name"`
	ResponseCount int            `json:"response_count"`
	Conditions    map[string]int `json:"conditions"`
	Counts        []int          `json:"counts"`
}

type robustArtifactsFile struct {
	ModelOrder    []string            `json:"model_order"`
	RobustReady   bool                `json:"robust_ready"`
	Hellinger     robustHellingerFile `json:"hellinger"`
	OrderedBlocks robustOrderedFile   `json:"ordered_blocks"`
}

type robustHellingerFile struct {
	FeatureMean   []float64   `json:"feature_mean"`
	FeatureScale  []float64   `json:"feature_scale"`
	NuisanceBasis [][]float64 `json:"nuisance_basis"`
	Centroids     [][]float64 `json:"centroids"`
}

type robustOrderedFile struct {
	Weight               float64       `json:"weight"`
	FeatureMean          []float64     `json:"feature_mean"`
	FeatureScale         []float64     `json:"feature_scale"`
	NuisanceBasis        [][]float64   `json:"nuisance_basis"`
	Centroids            [][]float64   `json:"centroids"`
	EnvironmentCentroids [][][]float64 `json:"environment_centroids"`
}

type robustCalibration struct {
	Beta       float64 `json:"beta"`
	CVAccuracy float64 `json:"cv_accuracy"`
	CVCorrect  int     `json:"cv_correct"`
	CVSamples  int     `json:"cv_samples"`
}

// RobustBankModel 稳健库单模型条目。
type RobustBankModel struct {
	ID            string
	DisplayName   string
	Family        string
	FamilyName    string
	ResponseCount int
	Counts        []int // 36 响应合计的 355 维计数直方图
}

// RobustBank 解析并校验后的稳健指纹库。
type RobustBank struct {
	builtAt     string
	models      []RobustBankModel
	hellinger   robustHellingerFile
	ordered     robustOrderedFile
	calibration map[int]robustCalibration // 1..3 查询档
}

var (
	robustBankOnce sync.Once
	robustBankInst *RobustBank
	robustBankErr  error
)

// EmbeddedRobustBank 返回内嵌稳健指纹库（进程内单次解析缓存）。
func EmbeddedRobustBank() (*RobustBank, error) {
	robustBankOnce.Do(func() {
		robustBankInst, robustBankErr = ParseRobustBank(robustBankJSON)
	})
	return robustBankInst, robustBankErr
}

// ParseRobustBank 解析并校验稳健指纹库 JSON。
func ParseRobustBank(data []byte) (*RobustBank, error) {
	var file robustBankFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse robust bank: %w", err)
	}
	if len(file.Models) < 2 {
		return nil, fmt.Errorf("robust bank 模型数不足（%d）", len(file.Models))
	}
	n := len(file.Models)
	if len(file.Robust.ModelOrder) != n {
		return nil, fmt.Errorf("robust bank model_order 长度 %d 与模型数 %d 不符", len(file.Robust.ModelOrder), n)
	}
	models := make([]RobustBankModel, 0, n)
	for i, m := range file.Models {
		if m.ID == "" || m.ID != file.Robust.ModelOrder[i] {
			return nil, fmt.Errorf("robust bank 模型 %d 与 model_order 不一致", i)
		}
		if len(m.Counts) != MaxValue {
			return nil, fmt.Errorf("robust bank 模型 %s 计数直方图维度 %d", m.ID, len(m.Counts))
		}
		models = append(models, RobustBankModel{
			ID: m.ID, DisplayName: m.DisplayName, Family: m.Family,
			FamilyName: m.FamilyName, ResponseCount: m.ResponseCount, Counts: m.Counts,
		})
	}
	h := file.Robust.Hellinger
	if len(h.FeatureMean) != MaxValue || len(h.FeatureScale) != MaxValue {
		return nil, fmt.Errorf("robust bank hellinger 标准化参数维度异常")
	}
	for _, v := range h.NuisanceBasis {
		if len(v) != MaxValue {
			return nil, fmt.Errorf("robust bank hellinger 扰动基维度异常")
		}
	}
	if len(h.Centroids) != n {
		return nil, fmt.Errorf("robust bank hellinger 质心数 %d 与模型数 %d 不符", len(h.Centroids), n)
	}
	for _, c := range h.Centroids {
		if len(c) != MaxValue {
			return nil, fmt.Errorf("robust bank hellinger 质心维度异常")
		}
	}
	o := file.Robust.OrderedBlocks
	if len(o.FeatureMean) != OrderedDims || len(o.FeatureScale) != OrderedDims {
		return nil, fmt.Errorf("robust bank ordered 标准化参数维度异常")
	}
	for _, v := range o.NuisanceBasis {
		if len(v) != OrderedDims {
			return nil, fmt.Errorf("robust bank ordered 扰动基维度异常")
		}
	}
	if len(o.Centroids) != n {
		return nil, fmt.Errorf("robust bank ordered 质心数 %d 与模型数 %d 不符", len(o.Centroids), n)
	}
	for _, c := range o.Centroids {
		if len(c) != OrderedDims {
			return nil, fmt.Errorf("robust bank ordered 质心维度异常")
		}
	}
	if len(o.EnvironmentCentroids) == 0 {
		return nil, fmt.Errorf("robust bank 缺少环境质心")
	}
	for _, env := range o.EnvironmentCentroids {
		if len(env) != n {
			return nil, fmt.Errorf("robust bank 环境质心模型数不符")
		}
		for _, c := range env {
			if len(c) != OrderedDims {
				return nil, fmt.Errorf("robust bank 环境质心维度异常")
			}
		}
	}
	if o.Weight < 0 || o.Weight > 1 {
		return nil, fmt.Errorf("robust bank ordered 权重异常 %v", o.Weight)
	}
	calibration := make(map[int]robustCalibration, len(file.Calibration))
	for key, cal := range file.Calibration {
		q, err := strconv.Atoi(key)
		if err != nil || cal.Beta <= 0 {
			return nil, fmt.Errorf("robust bank 校准档 %q 异常", key)
		}
		calibration[q] = cal
	}
	if _, ok := calibration[1]; !ok {
		return nil, fmt.Errorf("robust bank 缺少 1 查询校准档")
	}
	return &RobustBank{
		builtAt: file.BuiltAt, models: models,
		hellinger: h, ordered: o, calibration: calibration,
	}, nil
}

// ModelIDs 库内模型 ID（有序）。
func (b *RobustBank) ModelIDs() []string {
	out := make([]string, 0, len(b.models))
	for _, m := range b.models {
		out = append(out, m.ID)
	}
	return out
}

// ModelCount 库内模型数。
func (b *RobustBank) ModelCount() int { return len(b.models) }

// HasModel 模型是否在库内。
func (b *RobustBank) HasModel(id string) bool {
	for _, m := range b.models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// BuiltAt 库构建时间（原始字符串）。
func (b *RobustBank) BuiltAt() string { return b.builtAt }

// CalibrationBeta 有效查询数对应档的 softmax 温度（封顶 3；缺档回落 1 档）。
func (b *RobustBank) CalibrationBeta(queries int) float64 {
	if queries < 1 {
		queries = 1
	}
	if queries > 3 {
		queries = 3
	}
	for ; queries >= 1; queries-- {
		if cal, ok := b.calibration[queries]; ok {
			return cal.Beta
		}
	}
	return 1
}

// ModelCounts 模型的 355 维计数直方图副本（库内没有时 ok=false）。
func (b *RobustBank) ModelCounts(id string) ([]int, bool) {
	for _, m := range b.models {
		if m.ID == id {
			out := make([]int, len(m.Counts))
			copy(out, m.Counts)
			return out, true
		}
	}
	return nil, false
}

// CVAccuracy 校准档的交叉验证准确率（展示用）。
func (b *RobustBank) CVAccuracy(queries int) float64 {
	if queries < 1 {
		queries = 1
	}
	if queries > 3 {
		queries = 3
	}
	if cal, ok := b.calibration[queries]; ok {
		return cal.CVAccuracy
	}
	return 0
}

// ---- 评分引擎（与上游 fingerprint-core.mjs / sub4api codex_fingerprint.go 逐句对齐）----

func robustDot(left, right []float64) float64 {
	var v float64
	for i := range left {
		v += left[i] * right[i]
	}
	return v
}

func robustNorm(values []float64) float64 {
	return math.Sqrt(robustDot(values, values))
}

func robustUnit(values []float64) []float64 {
	scale := math.Max(robustNorm(values), 1e-12)
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = v / scale
	}
	return out
}

// robustZScore 跨模型 z 标准化（总体标准差，下限 1e-12）。
func robustZScore(values []float64) []float64 {
	n := float64(len(values))
	if n == 0 {
		return values
	}
	var mean float64
	for _, v := range values {
		mean += v
	}
	mean /= n
	var variance float64
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	scale := math.Max(math.Sqrt(variance/n), 1e-12)
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = (v - mean) / scale
	}
	return out
}

// robustSubtractBasis 依次减去基向量的投影分量（扰动子空间投影消除）。
func robustSubtractBasis(values []float64, basis [][]float64) []float64 {
	out := append([]float64(nil), values...)
	for _, vector := range basis {
		projection := robustDot(out, vector)
		for i := range out {
			out[i] -= projection * vector[i]
		}
	}
	return out
}

// robustProject 全局标准化 (v-mean)/scale。
func robustProject(values, mean, scale []float64) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = (v - mean[i]) / scale[i]
	}
	return out
}

// robustHellingerFeature 355 维 Hellinger 特征：sqrt((c+α)/(N+αK))，α=0.5。
func robustHellingerFeature(counts []float64) []float64 {
	var total float64
	for _, c := range counts {
		total += c
	}
	total += Alpha * float64(MaxValue)
	out := make([]float64, len(counts))
	for i, c := range counts {
		out[i] = math.Sqrt((c + Alpha) / total)
	}
	return out
}

// robustOrderedBlockFeature 74 维顺序块特征：4 段×16 值桶 + 10 个尾数桶，
// 桶均带 0.5 先验，sqrt(v/total) 归一。与自建库的 OrderedBlockFeature
// （原始计数+均值/方差）是两套不同的特征定义，不可混用。
func robustOrderedBlockFeature(numbers []int) []float64 {
	feature := make([]float64, 0, OrderedDims)
	base, remainder, start := len(numbers)/4, len(numbers)%4, 0
	for block := 0; block < 4; block++ {
		size := base
		if block < remainder {
			size++
		}
		bins := make([]float64, 16)
		for i := range bins {
			bins[i] = 0.5
		}
		for _, v := range numbers[start : start+size] {
			bins[valueBin(v)]++
		}
		denominator := float64(size) + 8 // 16 桶 × 0.5 先验
		for _, v := range bins {
			feature = append(feature, math.Sqrt(v/denominator))
		}
		start += size
	}
	lastDigits := make([]float64, 10)
	for i := range lastDigits {
		lastDigits[i] = 0.5
	}
	for _, n := range numbers {
		lastDigits[n%10]++
	}
	denominator := float64(len(numbers)) + 5 // 10 桶 × 0.5 先验
	for _, v := range lastDigits {
		feature = append(feature, math.Sqrt(v/denominator))
	}
	return feature
}

// robustMarginalScores 边际分量：Hellinger 特征 → 标准化 → 去扰动 → 单位化
// → 质心点积 → z 标准化。
func (b *RobustBank) robustMarginalScores(counts []float64) []float64 {
	artifact := b.hellinger
	feature := robustHellingerFeature(counts)
	projected := robustProject(feature, artifact.FeatureMean, artifact.FeatureScale)
	projected = robustSubtractBasis(projected, artifact.NuisanceBasis)
	projected = robustUnit(projected)
	scores := make([]float64, len(artifact.Centroids))
	for i, centroid := range artifact.Centroids {
		scores[i] = robustDot(projected, centroid)
	}
	return robustZScore(scores)
}

// robustOrderedScores 顺序块分量：环境质心 max 池化模板分 0.5 + 去扰后质心
// 分 0.5，再 z 标准化。
func (b *RobustBank) robustOrderedScores(numbers []int) []float64 {
	artifact := b.ordered
	feature := robustOrderedBlockFeature(numbers)
	standardized := robustProject(feature, artifact.FeatureMean, artifact.FeatureScale)
	unit := robustUnit(standardized)
	templateRaw := make([]float64, len(artifact.Centroids))
	for i := range templateRaw {
		templateRaw[i] = math.Inf(-1)
		for _, environment := range artifact.EnvironmentCentroids {
			if s := robustDot(unit, environment[i]); s > templateRaw[i] {
				templateRaw[i] = s
			}
		}
	}
	template := robustZScore(templateRaw)
	nuisanceFeature := robustUnit(robustSubtractBasis(standardized, artifact.NuisanceBasis))
	nuisanceRaw := make([]float64, len(artifact.Centroids))
	for i, centroid := range artifact.Centroids {
		nuisanceRaw[i] = robustDot(nuisanceFeature, centroid)
	}
	nuisance := robustZScore(nuisanceRaw)
	fused := make([]float64, len(template))
	for i := range fused {
		fused[i] = 0.5*template[i] + 0.5*nuisance[i]
	}
	return robustZScore(fused)
}

// robustScores 单回答对全库模型的融合分：(1−w)·边际 + w·顺序块。
func (b *RobustBank) robustScores(numbers []int) []float64 {
	counts := make([]float64, MaxValue)
	for _, n := range numbers {
		counts[n-1]++
	}
	marginal := b.robustMarginalScores(counts)
	weight := b.ordered.Weight
	if weight == 0 {
		return marginal
	}
	ordered := b.robustOrderedScores(numbers)
	out := make([]float64, len(marginal))
	for i := range out {
		out[i] = (1-weight)*marginal[i] + weight*ordered[i]
	}
	return out
}

// Analyze 稳健库判定：过滤有效回答（阈值规则与自建库一致）→ 逐回答融合分
// 取均值 → softmax（温度取 min(有效数,3) 档校准 β）。语义对齐 Bank.Analyze。
func (b *RobustBank) Analyze(answers []Answer) (*Verdict, error) {
	if b == nil {
		return nil, fmt.Errorf("robust bank 未加载")
	}
	verdict := &Verdict{}
	valid := make([]Answer, 0, len(answers))
	for i, a := range answers {
		threshold := ValidAnswerThreshold(a.ExpectedCount)
		if len(a.Numbers) < threshold {
			verdict.Reasons = append(verdict.Reasons,
				fmt.Sprintf("回答 %d 数字不足（%d < 阈值 %d）", i+1, len(a.Numbers), threshold))
			continue
		}
		valid = append(valid, a)
	}
	verdict.ValidRuns = len(valid)
	if len(valid) == 0 {
		verdict.Reasons = append(verdict.Reasons, "无有效回答，无法判定")
		return verdict, nil
	}

	combined := make([]float64, len(b.models))
	perAnswer := make([][]float64, len(valid))
	for i, a := range valid {
		perAnswer[i] = b.robustScores(a.Numbers)
		for j, v := range perAnswer[i] {
			combined[j] += v
		}
	}
	for j := range combined {
		combined[j] /= float64(len(valid))
	}

	beta := b.CalibrationBeta(len(valid))
	probs := softmax(beta, combined)
	winner := argmax(combined)
	verdict.Model = b.models[winner].ID
	verdict.Probability = probs[winner]
	for _, pa := range perAnswer {
		if argmax(pa) == winner {
			verdict.TopHits++
		}
	}
	for i, m := range b.models {
		verdict.PerModel = append(verdict.PerModel, ModelScore{
			Model:       m.ID,
			Combined:    combined[i],
			Probability: probs[i],
		})
	}
	return verdict, nil
}

// ---- 挑战生成器（上游模板语料，与建库环境同源）----

const (
	robustChallengeMinCount = 292
	robustChallengeMaxCount = 332
)

// robustChallengeTemplates 上游 ModelTrace 的四组模板语料（5 开头 × 5 动作
// × 5 收尾 × 4 分隔 = 500 种组合 × 41 个长度）。稳健库的顺序块环境质心按
// 这些采集环境池化——稳健库路径的检测/采集必须用本生成器，不能用自建库
// 的 GenerateChallenges（语料不同会引入环境偏移）。
var robustChallengeTemplates = [...][]string{
	{
		"这是一次独立的数值选择记录",
		"请完成下面的无语义整数选择任务",
		"执行一次第一反应取值记录",
		"生成一组不承载语义的整数选择",
		"进行一轮快速逐项取值",
	},
	{
		"为各个位置分别凭第一反应选择",
		"逐项选择",
		"每次只决定当前一项，共给出",
		"分别凭第一反应给出",
		"逐个直接选择",
	},
	{
		"允许某个数字再次出现；每项写出后不要回头排序、去重或替换。",
		"偶然重复是有效的；不要重新排列或修正已经写出的项目。",
		"相同值可以再次出现；输出过程中不要整理或改写前面的项目。",
		"重复值无需删除；不要筛选、重排或补成某种规律。",
		"不必赋予数字任何含义；已经给出的值保持不变。",
	},
	{
		"数字之间用逗号或空格分隔均可。",
		"使用一种一致的常见分隔符即可。",
		"可以用逗号、空格或换行分隔。",
		"只要每个整数边界清楚，格式可自行选择。",
	},
}

// GenerateRobustChallenges 生成 n 个上游模板挑战：长度在 [292,332] 独立
// 均匀抽取（上游 Go 实现语义，不要求跨查询不重复），模板各组独立抽取。
// rnd 语义同 GenerateChallenges（生产 CryptoRandIntn，测试可注入）。
func GenerateRobustChallenges(n int, rnd func(int) int) []Challenge {
	if n < 0 {
		n = 0
	}
	out := make([]Challenge, 0, n)
	span := robustChallengeMaxCount - robustChallengeMinCount + 1
	for i := 0; i < n; i++ {
		count := robustChallengeMinCount + rnd(span)%span
		pick := func(pool []string) string {
			return pool[rnd(len(pool))%len(pool)]
		}
		prompt := fmt.Sprintf("%s。%s %d 个 1 到 355（含端点）的整数。",
			pick(robustChallengeTemplates[0]), pick(robustChallengeTemplates[1]), count) +
			"每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。" +
			"本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。" +
			pick(robustChallengeTemplates[2]) + pick(robustChallengeTemplates[3]) +
			"直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。"
		out = append(out, Challenge{ExpectedCount: count, Prompt: prompt})
	}
	return out
}
