package pricingrules

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestSelect 检查官方档位的输入与输出边界以及写入总数和TTL明细重叠的情况。
// 同一请求只选择一个输出档，十二小时缓存按总写入量中的实际子集加价。
func TestSelect(t *testing.T) {
	rules, err := Parse([]byte(`{"version":1,"models":{"glm-4.5-air":{"output_tiers":[{"min_input_tokens":32000,"min_output_tokens":0,"input_multiplier":1.5,"output_multiplier":4,"cache_read_multiplier":1.5},{"min_input_tokens":0,"min_output_tokens":200,"output_multiplier":3}]},"gpt-6.1-sol":{"cache_write_12h_multiplier":2},"qwen3-14b":{"thinking_output_multiplier":2.5,"thinking_default":true}}}`))
	require.NoError(t, err)
	cases := []struct {
		model           string
		in, out, writes int
		facts           Facts
		want            Factors
	}{
		{"GM/glm-4.5-air", 31999, 199, 0, Facts{}, Factors{1, 1, 1, 1}},
		{"glm-4.5-air", 31999, 200, 0, Facts{}, Factors{1, 3, 1, 1}},
		{"glm-4.5-air", 32000, 199, 0, Facts{}, Factors{1.5, 4, 1.5, 1}},
		{"gpt-6.1-sol", 2000, 10, 1000, Facts{CacheWrite12hTokens: 250}, Factors{1, 1, 1, 1.25}},
		{"gpt-6.1-sol", 2000, 10, 1000, Facts{CacheWrite12hTokens: 2000}, Factors{1, 1, 1, 2}},
		{"qwen3-14b", 100, 10, 0, Facts{}, Factors{1, 2.5, 1, 1}},
		{"qwen3-14b", 100, 10, 0, Facts{ThinkingMode: "disabled"}, Factors{1, 1, 1, 1}},
		{"qwen3-14b", 100, 10, 0, Facts{ThinkingMode: "disabled", ReasoningTokens: 1}, Factors{1, 2.5, 1, 1}},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, rules.Select(tc.model, tc.in, tc.out, tc.writes, tc.facts), tc.model)
	}
}

// TestHolidayCalendar 检查国庆、普通工作日及尚未发布年份的计费选择。
// 日期以北京时间为准，日历未覆盖的年份按低峰收费，直到部署新的官方日历快照。
func TestHolidayCalendar(t *testing.T) {
	r, err := Parse([]byte(`{"version":1,"models":{"deepseek-v4-pro":{"holiday_off_peak":true}},"china_holidays":{"2026":["2026-10-01"]}}`))
	require.NoError(t, err)
	require.True(t, r.SkipPeak("GM/deepseek-v4-pro", time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)))
	require.False(t, r.SkipPeak("deepseek-v4-pro", time.Date(2026, 10, 30, 2, 0, 0, 0, time.UTC)))
	require.True(t, r.SkipPeak("deepseek-v4-pro", time.Date(2027, 1, 1, 2, 0, 0, 0, time.UTC)))
	require.True(t, ChinaHoliday2026(time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)))
}

// TestFacts 检查上游明细和请求模式被提取为有限的计费证据。
// metadata中的伪同名字段不能触发折扣，内容块中的有效显式缓存标记可以触发。
func TestFacts(t *testing.T) {
	f := UsageFacts(gjson.Parse(`{"input_tokens_details":{"cache_write_tokens_details":{"ttl_12h_tokens":12}},"output_tokens_details":{"reasoning_tokens":4}}`))
	require.Equal(t, 12, f.CacheWrite12hTokens)
	require.Equal(t, 4, f.ReasoningTokens)
	require.False(t, RequestFacts([]byte(`{"metadata":{"cache_control":{"type":"ephemeral"}}}`)).ExplicitCache)
	req := RequestFacts([]byte(`{"enable_thinking":false,"messages":[{"content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}]}]}`))
	require.True(t, req.ExplicitCache)
	f.MergeRequest(req)
	require.Equal(t, "disabled", f.ThinkingMode)
	require.Equal(t, 12, f.CacheWrite12hTokens)
}

// TestValidation 检查不支持的字段、错误日期和负倍率在发布前被拒绝。
// 错误的规则快照不能被解释成一组看似有效的普通价，防止静默计费漂移。
func TestValidation(t *testing.T) {
	for _, data := range []string{`{"version":2}`, `{"version":1,"unknown":1}`, `{"version":1,"models":{"x":{"thinking_output_multiplier":-1}}}`, `{"version":1,"china_holidays":{"2026":["2026-02-30"]}}`} {
		_, err := Parse([]byte(data))
		require.Error(t, err)
	}
}
