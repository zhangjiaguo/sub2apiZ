package modeltrace

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// 合成分布生成器：模拟各模型对「随机整数」的稳定个体偏差。
func genUniform(r *rand.Rand, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 1 + r.Intn(MaxValue)
	}
	return out
}

func genLowSkew(r *rand.Rand, n int) []int {
	out := make([]int, n)
	for i := range out {
		if r.Intn(10) < 8 {
			out[i] = 1 + r.Intn(100) // 80% 集中在 1..100
		} else {
			out[i] = 1 + r.Intn(MaxValue)
		}
	}
	return out
}

func genHighSkew(r *rand.Rand, n int) []int {
	out := make([]int, n)
	for i := range out {
		if r.Intn(10) < 8 {
			out[i] = 256 + r.Intn(100) // 80% 集中在 256..355
		} else {
			out[i] = 1 + r.Intn(MaxValue)
		}
	}
	return out
}

func genSevens(r *rand.Rand, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 7 * (1 + r.Intn(50)) // 7 的倍数偏好
		if out[i] > MaxValue {
			out[i] = MaxValue
		}
	}
	return out
}

// genAscending 边际与 uniform 相同但输出升序（检验顺序块特征的区分力）。
func genAscending(r *rand.Rand, n int) []int {
	out := genUniform(r, n)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func synthSamples(r *rand.Rand, models map[string]func(*rand.Rand, int) []int, per int) []Sample {
	var out []Sample
	for m, gen := range models {
		for i := 0; i < per; i++ {
			n := 292 + r.Intn(41)
			out = append(out, Sample{Model: m, ExpectedCount: n, Numbers: gen(r, n)})
		}
	}
	return out
}

func answersFrom(r *rand.Rand, gen func(*rand.Rand, int) []int, k int) []Answer {
	var out []Answer
	for i := 0; i < k; i++ {
		n := 292 + r.Intn(41)
		out = append(out, Answer{ExpectedCount: n, Numbers: gen(r, n)})
	}
	return out
}

func buildTestBank(t *testing.T, per int) (*Bank, map[string]func(*rand.Rand, int) []int, *rand.Rand) {
	t.Helper()
	models := map[string]func(*rand.Rand, int) []int{
		"m-uniform": genUniform,
		"m-low":     genLowSkew,
		"m-high":    genHighSkew,
		"m-sevens":  genSevens,
		"m-asc":     genAscending,
	}
	r := rand.New(rand.NewSource(42))
	bank, err := BuildBank(synthSamples(r, models, per))
	if err != nil {
		t.Fatalf("BuildBank: %v", err)
	}
	return bank, models, r
}

func TestBuildBankProfiles(t *testing.T) {
	bank, _, _ := buildTestBank(t, 16)
	if len(bank.Profiles) != 5 {
		t.Fatalf("profiles=%d want 5", len(bank.Profiles))
	}
	for _, p := range bank.Profiles {
		if p.Samples != 16 {
			t.Fatalf("%s samples=%d want 16", p.Model, p.Samples)
		}
		if len(p.Marginal) != MaxValue || len(p.OrderedCentroid) != OrderedDims || len(p.OrderedVariance) != OrderedDims {
			t.Fatalf("%s dims wrong", p.Model)
		}
		var sum float64
		for _, c := range p.Marginal {
			sum += c
		}
		if sum < 280 || sum > 340 { // 平均计数 ≈ 平均长度 312
			t.Fatalf("%s marginal sum=%v", p.Model, sum)
		}
	}
	for g := 1; g <= 3; g++ {
		if b, ok := bank.Calibration[g]; !ok || b <= 0 {
			t.Fatalf("calibration[%d]=%v missing/invalid", g, b)
		}
	}
}

func TestAnalyzeIdentifiesEachModel(t *testing.T) {
	bank, models, r := buildTestBank(t, 16)
	for m, gen := range models {
		verdict, err := bank.Analyze(answersFrom(r, gen, 3))
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if verdict.Model != m {
			t.Fatalf("requested-family %s misidentified as %s (prob %.3f)", m, verdict.Model, verdict.Probability)
		}
		if verdict.Probability < 0.6 {
			t.Fatalf("%s low confidence %.3f", m, verdict.Probability)
		}
		if verdict.TopHits == 0 {
			t.Fatalf("%s zero top hits", m)
		}
		if verdict.ValidRuns != 3 {
			t.Fatalf("%s validRuns=%d", m, verdict.ValidRuns)
		}
	}
}

func TestAnalyzeSingleAnswer(t *testing.T) {
	bank, _, r := buildTestBank(t, 16)
	verdict, err := bank.Analyze(answersFrom(r, genLowSkew, 1))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Model != "m-low" {
		t.Fatalf("single answer verdict %s", verdict.Model)
	}
}

func TestAnalyzeOrderedOnlyDiscrimination(t *testing.T) {
	// m-uniform 与 m-asc 边际分布相同，仅顺序不同：判定必须依赖顺序块特征。
	bank, models, r := buildTestBank(t, 16)
	for m, gen := range models {
		if m != "m-uniform" && m != "m-asc" {
			continue
		}
		verdict, err := bank.Analyze(answersFrom(r, gen, 3))
		if err != nil {
			t.Fatal(err)
		}
		if verdict.Model != m {
			t.Fatalf("ordered discrimination failed for %s → %s", m, verdict.Model)
		}
	}
}

func TestAnalyzeTooFewNumbers(t *testing.T) {
	bank, _, _ := buildTestBank(t, 16)
	short := []Answer{
		{ExpectedCount: 300, Numbers: genUniform(rand.New(rand.NewSource(1)), 100)}, // < 165 阈值
		{ExpectedCount: 300, Numbers: genUniform(rand.New(rand.NewSource(2)), 50)},
	}
	verdict, err := bank.Analyze(short)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Model != "" || verdict.ValidRuns != 0 {
		t.Fatalf("should be inconclusive: %+v", verdict)
	}
	if len(verdict.Reasons) < 3 { // 两条不足 + 一条总结
		t.Fatalf("reasons=%v", verdict.Reasons)
	}
}

func TestBuildBankInsufficientSamples(t *testing.T) {
	m := map[string]func(*rand.Rand, int) []int{
		"ok-model":   genUniform,
		"thin-model": genLowSkew,
	}
	r := rand.New(rand.NewSource(7))
	var samples []Sample
	for name, gen := range m {
		per := 16
		if name == "thin-model" {
			per = 3
		}
		for i := 0; i < per; i++ {
			n := 300
			samples = append(samples, Sample{Model: name, ExpectedCount: n, Numbers: gen(r, n)})
		}
	}
	if _, err := BuildBank(samples); err == nil {
		t.Fatal("insufficient samples must fail")
	} else if !strings.Contains(err.Error(), "thin-model") {
		t.Fatalf("error should name the thin model: %v", err)
	}
}

func TestBankJSONRoundTrip(t *testing.T) {
	bank, models, r := buildTestBank(t, 16)
	data, err := MarshalBank(bank)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseBank(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Profiles) != len(bank.Profiles) {
		t.Fatalf("profiles %d vs %d", len(parsed.Profiles), len(bank.Profiles))
	}
	for g, b := range bank.Calibration {
		if got := parsed.Calibration[g]; math.Abs(got-b) > 1e-12 {
			t.Fatalf("calibration[%d] drift %v vs %v", g, got, b)
		}
	}
	for m, gen := range models {
		answers := answersFrom(r, gen, 3)
		v1, _ := bank.Analyze(answers)
		v2, _ := parsed.Analyze(answers)
		if v1.Model != v2.Model || math.Abs(v1.Probability-v2.Probability) > 1e-9 {
			t.Fatalf("%s: roundtrip drift %s/%.4f vs %s/%.4f", m, v1.Model, v1.Probability, v2.Model, v2.Probability)
		}
	}
}

func TestParseBankRejectsEmpty(t *testing.T) {
	if _, err := ParseBank([]byte(`{}`)); err == nil {
		t.Fatal("empty bank must fail")
	}
	if _, err := ParseBank([]byte(`not json`)); err == nil {
		t.Fatal("bad json must fail")
	}
}

func TestCalibrationReasonable(t *testing.T) {
	bank, models, r := buildTestBank(t, 20)
	// 校准后的 β 应当让同族判定高置信：全部模型 3 回答判定概率平均 > 0.8。
	var probs []float64
	for m, gen := range models {
		v, err := bank.Analyze(answersFrom(r, gen, 3))
		if err != nil {
			t.Fatal(err)
		}
		if v.Model != m {
			t.Fatalf("%s → %s", m, v.Model)
		}
		probs = append(probs, v.Probability)
	}
	var mean float64
	for _, p := range probs {
		mean += p
	}
	mean /= float64(len(probs))
	if mean < 0.8 {
		t.Fatalf("mean calibrated probability %.3f too low (%v)", mean, probs)
	}
}
