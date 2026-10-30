//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pricingrules"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestOfficialDeepSeekHoliday 不经过网络，复现国庆节仍被收取高峰价的问题。
// 工作日同一时刻继续收取高峰价，防止修复时关闭全部峰谷计费。
func TestOfficialDeepSeekHoliday(t *testing.T) {
	require.Equal(t, 1.0, deepseekPeakMultiplierAt(time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)))
	require.Equal(t, 2.0, deepseekPeakMultiplierAt(time.Date(2026, 10, 30, 2, 0, 0, 0, time.UTC)))
}

// TestOfficialDefaultCalendar 检查默认目录也遵守未知年份低峰规则，避免只豁免渠道价卡。
func TestOfficialDefaultCalendar(t *testing.T) {
	rules, err := pricingrules.Parse([]byte(`{"version":1,"models":{"deepseek-v4-flash":{"holiday_off_peak":true}},"china_holidays":{"2026":["2026-10-01"]}}`))
	require.NoError(t, err)
	bs := &BillingService{tokenRules: rules}
	resolved := &ResolvedPricing{Mode: BillingModeToken, Source: PricingSourceLiteLLM, BasePricing: &ModelPricing{InputPricePerToken: 1e-6}}
	cost, err := bs.CalculateCostUnified(CostInput{Model: "deepseek-v4-flash", Tokens: UsageTokens{InputTokens: 1000}, RateMultiplier: 1, PricingAt: time.Date(2027, 1, 1, 2, 0, 0, 0, time.UTC), Resolver: NewModelPricingResolver(nil, bs), Resolved: resolved})
	require.NoError(t, err)
	require.InDelta(t, 0.00015, cost.ActualCost, 1e-12)
}

// TestOfficialLegacyTTL 检查没有Resolver的兼容调用同样应用缓存寿命规则。
func TestOfficialLegacyTTL(t *testing.T) {
	rules, err := pricingrules.Parse([]byte(`{"version":1,"models":{"gpt-6.1-sol":{"cache_write_12h_multiplier":2}}}`))
	require.NoError(t, err)
	bs := &BillingService{tokenRules: rules, fallbackPrices: map[string]*ModelPricing{"gpt-6.1-sol": {InputPricePerToken: 1e-6, CacheCreationPricePerToken: 2e-6, CacheCreationPriceExplicit: true}}}
	cost, err := bs.CalculateCostUnified(CostInput{Model: "gpt-6.1-sol", Tokens: UsageTokens{InputTokens: 10, CacheCreationTokens: 1000, PricingDetails: pricingrules.Facts{CacheWrite12hTokens: 250}}, RateMultiplier: 1.5})
	require.NoError(t, err)
	require.InDelta(t, 0.003765, cost.ActualCost, 1e-12)
}

// TestOfficialOpenAIWriteTTL 复现响应中的十二小时缓存写入明细被丢弃的问题。
// 断言解析后的计费用量仍携带明细，保证后续计价能区分两种缓存寿命。
func TestOfficialOpenAIWriteTTL(t *testing.T) {
	usage, ok := openAIUsageFromGJSON(gjson.Parse(`{"input_tokens":2000,"output_tokens":10,"input_tokens_details":{"cache_write_tokens":1000,"cache_write_tokens_details":{"ttl_30m_tokens":750,"ttl_12h_tokens":250}}}`))
	require.True(t, ok)
	require.Equal(t, 250, usage.PricingDetails.CacheWrite12hTokens)
}
