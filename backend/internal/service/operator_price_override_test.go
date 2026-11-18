//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOperatorDeepseekRatesSurviveDefaultPolicy(t *testing.T) {
	for _, tc := range []struct {
		model               string
		input, output, read float64
		peak                bool
	}{
		{"deepseek-v4.1-flash-1017", 1e-6, 4e-6, .02e-6, true},
		{"deepseek-v4-pro-0813", 4.5e-6, 13.5e-6, .15e-6, true},
		{"deepseek-v3.2", 2e-6, 3e-6, .2e-6, false},
		{"deepseek-v3.2-exp", 2e-6, 3e-6, .2e-6, false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Pricing.OverrideFile = filepath.Join(t.TempDir(), "operator.json")
			raw := []byte(`{"` + tc.model + `":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000004,"cache_read_input_token_cost":0.00000002}}`)
			if !tc.peak {
				raw = []byte(`{"` + tc.model + `":{"input_cost_per_token":0.000002,"output_cost_per_token":0.000003,"cache_read_input_token_cost":0.0000002}}`)
			}
			if tc.model == "deepseek-v4-pro-0813" {
				raw = []byte(`{"` + tc.model + `":{"input_cost_per_token":0.0000045,"output_cost_per_token":0.0000135,"cache_read_input_token_cost":0.00000015}}`)
			}
			require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, raw, 0600))
			ps := &PricingService{cfg: cfg}
			data, _, err := ps.buildPricingData([]byte(`{"` + tc.model + `":{"input_cost_per_token":0.001,"output_cost_per_token":0.002}}`))
			require.NoError(t, err)
			ps.pricingData = data
			bs := NewBillingService(cfg, ps)
			quoted, err := bs.GetModelPricing(tc.model)
			require.NoError(t, err)
			require.InDelta(t, tc.input, quoted.InputPricePerToken, 1e-14)
			require.InDelta(t, tc.output, quoted.OutputPricePerToken, 1e-14)
			require.InDelta(t, tc.read, quoted.CacheReadPricePerToken, 1e-14)
			resolver := NewModelPricingResolver(nil, bs)
			base := 1000*tc.input + 200*tc.output + 500*tc.read
			for _, period := range []struct {
				name string
				at   time.Time
				mult float64
			}{
				{"off_peak", time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC), 1},
				{"peak", time.Date(2026, 11, 2, 6, 0, 0, 0, time.UTC), 2},
				{"weekend", time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC), 1},
				{"holiday", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC), 1},
			} {
				t.Run(period.name, func(t *testing.T) {
					mult := period.mult
					if !tc.peak {
						mult = 1
					}
					got, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: tc.model, Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 500}, RateMultiplier: 1.5, Resolver: resolver, PricingAt: period.at})
					require.NoError(t, err)
					require.InDelta(t, base*mult, got.TotalCost, 1e-12)
					require.InDelta(t, base*mult*1.5, got.ActualCost, 1e-12)
				})
			}
		})
	}
}

func TestIncompleteOperatorRatesKeepBuiltinDeepseekFallback(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.OverrideFile = filepath.Join(t.TempDir(), "partial.json")
	require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, []byte(`{"deepseek-v4-flash":{"input_cost_per_token":0.00001}}`), 0600))
	ps := &PricingService{cfg: cfg}
	data, _, err := ps.buildPricingData([]byte(`{"deepseek-v4-flash":{"input_cost_per_token":1,"output_cost_per_token":2}}`))
	require.NoError(t, err)
	ps.pricingData = data
	got, err := NewBillingService(cfg, ps).GetModelPricing("deepseek-v4-flash")
	require.NoError(t, err)
	require.Equal(t, deepseekFlashOffPeakInputPrice, got.InputPricePerToken)
}
