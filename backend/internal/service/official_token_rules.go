package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/pricingrules"

// official_token_rules.go 将已校验的供应商规则应用到互斥Token计费分项。
// 输入、输出、读取和写入已由计费器选好上下文价格，附加规则只调整对应分项。
// 重新汇总金额后再应用峰谷或推理档位倍率，使特殊规则与普通结算共用原来的用户倍率。

// applyOfficialTokenRules 根据完整输入上下文、实际输出长度和上游用量明细调整费用。
// 缓存寿命明细是写入总量的子集，普通输入不重复收费；保留图片独立分项与账本原有精度。
func applyOfficialTokenRules(rules *pricingrules.Rules, model string, pricing *ModelPricing, tokens UsageTokens, rate float64, cost *CostBreakdown) {
	if rules == nil || cost == nil {
		return
	}
	facts := tokens.PricingDetails
	if pricing != nil && pricing.CacheCreation12hPricePerToken != nil {
		if _, ok := openAICacheCreationTTLCost(tokens, 0, pricing.CacheCreation12hPricePerToken); ok {
			// 两种寿命已分别按原生价卡结算，旧规则只继续处理其他附加条件。
			facts.CacheWrite12hTokens = 0
		}
	}
	factors := rules.Select(model, tokens.InputTokens+tokens.CacheReadTokens+tokens.CacheCreationTokens, tokens.OutputTokens, tokens.CacheCreationTokens, facts)
	cost.InputCost *= factors.Input
	cost.OutputCost *= factors.Output
	cost.CacheReadCost *= factors.CacheRead
	cost.CacheCreationCost *= factors.CacheWrite
	cost.TotalCost = cost.InputCost + cost.OutputCost + cost.ImageInputCost + cost.ImageOutputCost + cost.CacheReadCost + cost.CacheCreationCost
	if rate < 0 {
		rate = 0
	}
	cost.ActualCost = cost.TotalCost * rate
}
