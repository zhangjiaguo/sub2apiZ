//go:build unit

package service

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pricingrules"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

// TestOfficialPriceAcceptance 使用公开官方报价和独立金额算例检验实际计费器。
// 覆盖输入输出档位、显隐式缓存、十二小时写入、节假日及分组倍率，阻止报价数值正确而结算路径错误。
func TestOfficialPriceAcceptance(t *testing.T) {
	read := func(name string) []byte {
		raw, err := os.ReadFile("testdata/official_pricing/" + name)
		require.NoError(t, err)
		return raw
	}
	var cards []ChannelModelPricing
	require.NoError(t, json.Unmarshal(read("prices.json"), &cards))
	lookup := map[string]*ChannelModelPricing{}
	for i := range cards {
		lookup[cards[i].Models[0]] = &cards[i]
	}
	rules, err := pricingrules.Parse(read("rules.json"))
	require.NoError(t, err)
	var cases []struct {
		Name     string             `json:"name"`
		Model    string             `json:"model"`
		Input    int                `json:"input"`
		Output   int                `json:"output"`
		Read     int                `json:"read"`
		Writes   int                `json:"writes"`
		Facts    pricingrules.Facts `json:"facts"`
		Rate     float64            `json:"rate"`
		At       string             `json:"at"`
		Tier     string             `json:"tier"`
		Units    int                `json:"units"`
		Expected float64            `json:"expected_usd"`
	}
	require.NoError(t, json.Unmarshal(read("cases.json"), &cases))

	bs := &BillingService{tokenRules: rules, fallbackPrices: map[string]*ModelPricing{}}
	resolver := NewModelPricingResolver(nil, bs)

	for _, tc := range cases {
		t.Run(tc.Model+"/"+tc.Name, func(t *testing.T) {
			card := lookup[tc.Model]
			require.NotNil(t, card)
			at, err := time.Parse(time.RFC3339, tc.At)
			require.NoError(t, err)

			resolved := resolver.resolveConfiguredPricing(card, tc.Model, PricingSourceChannel)
			resolved.longContextPricingEnabled = true
			got, err := bs.CalculateCostUnified(CostInput{Model: tc.Model, Tokens: UsageTokens{InputTokens: tc.Input, OutputTokens: tc.Output, CacheReadTokens: tc.Read, CacheCreationTokens: tc.Writes, PricingDetails: tc.Facts}, RateMultiplier: tc.Rate, RequestCount: tc.Units, ServiceTier: tc.Tier, PricingAt: at, Resolver: resolver, Resolved: resolved})

			require.NoError(t, err)
			require.InDelta(t, tc.Expected, got.ActualCost, 1e-10)
		})
	}
}
