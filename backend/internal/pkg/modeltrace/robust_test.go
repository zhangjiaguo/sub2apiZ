package modeltrace

import (
	"strings"
	"testing"
)

// loadRobustBankForTest 测试统一入口（内嵌库解析失败即 fail）。
func loadRobustBankForTest(t *testing.T) *RobustBank {
	t.Helper()
	bank, err := EmbeddedRobustBank()
	if err != nil {
		t.Fatalf("EmbeddedRobustBank: %v", err)
	}
	return bank
}

func TestRobustBankEmbeddedStructure(t *testing.T) {
	bank := loadRobustBankForTest(t)
	if bank.ModelCount() != 16 {
		t.Fatalf("模型数 = %d， want 16", bank.ModelCount())
	}
	for _, id := range []string{"gpt-5.4", "gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "claude-opus-5-5"} {
		if !bank.HasModel(id) {
			t.Errorf("库内缺少模型 %s", id)
		}
	}
	if bank.HasModel("gpt-4o") {
		t.Error("gpt-4o 不应在库内")
	}
	if bank.CalibrationBeta(1) <= 0 || bank.CalibrationBeta(3) <= 0 {
		t.Errorf("校准 β 异常: %v / %v", bank.CalibrationBeta(1), bank.CalibrationBeta(3))
	}
	if bank.CVAccuracy(3) < 0.99 {
		t.Errorf("3 查询档 CV 准确率 = %v， want >= 0.99", bank.CVAccuracy(3))
	}
	if bank.BuiltAt() == "" {
		t.Error("built_at 为空")
	}
}

// interleaveNumbers 把计数直方图展开成值轮询交错的数字序列（顺序块特征
// 需要顺序信息，按值排序的病态顺序会畸变顺序分量）。
func interleaveNumbers(counts []int) []int {
	snapshot := append([]int(nil), counts...)
	var out []int
	for remaining := true; remaining; {
		remaining = false
		for v := range snapshot {
			if snapshot[v] > 0 {
				out = append(out, v+1)
				snapshot[v]--
				remaining = true
			}
		}
	}
	return out
}

// 用库内模型自身的合计计数直方图重建一个「大回答」：其边际分布即该模型
// 的分布，评分应判回该模型（36 响应量级的聚合信号远超真实单次探测，是
// 引擎接线的强冒烟测试）。
func TestRobustBankAnalyzeReconstructsOwnModel(t *testing.T) {
	bank := loadRobustBankForTest(t)
	for _, target := range []string{"gpt-6-astra", "gpt-5.5", "gpt-6-sol"} {
		var model *RobustBankModel
		for i := range bank.models {
			if bank.models[i].ID == target {
				model = &bank.models[i]
				break
			}
		}
		if model == nil {
			t.Fatalf("模型 %s 不在库内", target)
		}
		numbers := interleaveNumbers(model.Counts)
		if len(numbers) < ValidAnswerThreshold(332) {
			t.Fatalf("%s 合计计数 %d 异常", target, len(numbers))
		}
		verdict, err := bank.Analyze([]Answer{{ExpectedCount: 300, Numbers: numbers}})
		if err != nil {
			t.Fatalf("%s Analyze: %v", target, err)
		}
		if verdict.Model != target {
			t.Errorf("%s 重建回答被判定为 %s（p=%.3f）", target, verdict.Model, verdict.Probability)
		}
		if verdict.ValidRuns != 1 || verdict.TopHits != 1 {
			t.Errorf("%s ValidRuns=%d TopHits=%d", target, verdict.ValidRuns, verdict.TopHits)
		}
	}
}

func TestRobustBankAnalyzeRejectsInsufficient(t *testing.T) {
	bank := loadRobustBankForTest(t)
	verdict, err := bank.Analyze([]Answer{{ExpectedCount: 300, Numbers: []int{1, 2, 3}}})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if verdict.Model != "" || verdict.ValidRuns != 0 {
		t.Errorf("数字不足不应给出判定: %+v", verdict)
	}
	if len(verdict.Reasons) == 0 {
		t.Error("应携带原因")
	}
	if _, err := bank.Analyze(nil); err != nil {
		t.Fatalf("空回答: %v", err)
	}
}

// 校准 β 档位：多回答封顶 3 档。
func TestRobustBankCalibrationBeta(t *testing.T) {
	bank := loadRobustBankForTest(t)
	if bank.CalibrationBeta(0) != bank.CalibrationBeta(1) {
		t.Error("0 查询应回落 1 档")
	}
	if bank.CalibrationBeta(5) != bank.CalibrationBeta(3) {
		t.Error("5 查询应封顶 3 档")
	}
}

func TestParseRobustBankRejectsBadInput(t *testing.T) {
	if _, err := ParseRobustBank([]byte(`{"schema":"x"}`)); err == nil {
		t.Error("缺模型应报错")
	}
	if _, err := ParseRobustBank([]byte(`not json`)); err == nil {
		t.Error("非法 JSON 应报错")
	}
}

// 上游模板挑战生成器：确定性 rnd 下可复现、结构完整、值域正确。
func TestGenerateRobustChallenges(t *testing.T) {
	seq := 0
	rnd := func(n int) int {
		v := seq % n
		seq++
		return v
	}
	challenges := GenerateRobustChallenges(3, rnd)
	if len(challenges) != 3 {
		t.Fatalf("生成 %d 个", len(challenges))
	}
	for _, c := range challenges {
		if c.ExpectedCount < 292 || c.ExpectedCount > 332 {
			t.Errorf("期望个数越界: %d", c.ExpectedCount)
		}
		if !strings.Contains(c.Prompt, "个 1 到 355（含端点）的整数。") {
			t.Errorf("提示语缺数量段: %s", c.Prompt)
		}
		if !containsAny(c.Prompt, robustChallengeTemplates[0]) {
			t.Errorf("提示语缺开头模板: %s", c.Prompt)
		}
		for _, fixed := range []string{
			"不要从 1 开始计数",
			"禁止调用或借助任何工具",
			"直接从第一个取值开始输出",
		} {
			if !strings.Contains(c.Prompt, fixed) {
				t.Errorf("提示语缺固定段 %q: %s", fixed, c.Prompt)
			}
		}
	}
	if n := GenerateRobustChallenges(-1, rnd); len(n) != 0 {
		t.Errorf("负数应返回空， got %d", len(n))
	}
	// rnd 恒 0 时也应可用（不 panic、结构完整）。
	for _, c := range GenerateRobustChallenges(2, func(int) int { return 0 }) {
		if c.ExpectedCount != 292 {
			t.Errorf("恒 0 rnd 期望 292， got %d", c.ExpectedCount)
		}
	}
}

func containsAny(s string, pool []string) bool {
	for _, p := range pool {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
