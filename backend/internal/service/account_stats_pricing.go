package service

import (
	"context"
	"strings"
)

// resolveAccountStatsCost 计算账号统计定价费用。
// 返回 nil 表示不覆盖，使用默认公式（total_cost × account_rate_multiplier）。
//
// 优先级（先命中为准）：
//  1. 自定义规则（始终尝试，不依赖 ApplyPricingToAccountStats 开关）
//  2. 模型定价文件中实际适用的 provider 长上下文价格
//  3. ApplyPricingToAccountStats 启用时，直接使用本次请求的客户计费（倍率前的 totalCost）
//  4. 模型定价文件（LiteLLM）中上游模型的默认价格
//  5. nil → 走默认公式（total_cost × account_rate_multiplier）
//
// upstreamModel 是最终发往上游的模型 ID。
// totalCost 是本次请求的客户计费（倍率前），用于优先级 3。
// serviceTier 是最终参与用户计费的 OpenAI 服务层级，用于 provider 定价。
type accountStatsCostUsage struct {
	requestCount int
	usageUnits   float64
	sizeTier     string
}

func resolveAccountStatsCostWithUsage(
	ctx context.Context,
	channelService *ChannelService,
	billingService *BillingService,
	accountID int64,
	groupID int64,
	upstreamModel string,
	tokens UsageTokens,
	usage accountStatsCostUsage,
	totalCost float64,
	serviceTier string,
) *float64 {
	if channelService == nil || upstreamModel == "" {
		return nil
	}
	channel, err := channelService.GetChannelForGroup(ctx, groupID)
	if err != nil || channel == nil {
		return nil
	}

	platform := channelService.GetGroupPlatform(ctx, groupID)

	// 优先级 1：自定义规则（始终尝试）
	if cost := tryCustomRulesWithUsage(channel, accountID, groupID, platform, upstreamModel, tokens, usage); cost != nil {
		return cost
	}

	// 账号是否向用户收取长上下文溢价，不改变 provider 的实际成本。
	// 因此实际命中的 provider 长上下文价格必须先于客户计费回退。
	if billingService != nil {
		if cost := tryLongContextModelFilePricing(billingService, upstreamModel, tokens, serviceTier); cost != nil {
			return cost
		}
	}

	// 优先级 3：渠道开启"应用模型定价到账号统计"时，直接使用客户计费（倍率前）
	if channel.ApplyPricingToAccountStats {
		cost := totalCost
		if cost <= 0 {
			return nil
		}
		return &cost
	}

	// 优先级 4：模型定价文件（LiteLLM）默认价格
	if billingService != nil {
		return tryModelFilePricing(billingService, upstreamModel, tokens, serviceTier)
	}

	return nil
}

func tryLongContextModelFilePricing(billingService *BillingService, model string, tokens UsageTokens, serviceTier string) *float64 {
	pricing, err := billingService.GetModelPricing(model)
	if err != nil || pricing == nil || !billingService.shouldApplySessionLongContextPricing(tokens, pricing) {
		return nil
	}
	breakdown, err := billingService.CalculateCostWithServiceTier(model, tokens, 1, normalizeBillingServiceTier(serviceTier))
	if err != nil || breakdown == nil || breakdown.TotalCost <= 0 {
		return nil
	}
	return &breakdown.TotalCost
}

// tryModelFilePricing 使用模型定价文件（LiteLLM/fallback）中的价格计算费用。
func tryModelFilePricing(billingService *BillingService, model string, tokens UsageTokens, serviceTier string) *float64 {
	pricing, err := billingService.GetModelPricing(model)
	if err != nil || pricing == nil {
		return nil
	}
	normalizedTier := normalizeBillingServiceTier(serviceTier)
	pricing = billingService.applyModelSpecificPricingPolicy(model, pricing)
	breakdown := billingService.computeTokenBreakdown(pricing, tokens, 1, normalizedTier, true)
	if breakdown == nil || breakdown.TotalCost <= 0 {
		return nil
	}
	return &breakdown.TotalCost
}

// tryCustomRulesWithUsage 遍历自定义规则，按数组顺序先命中为准。
func tryCustomRulesWithUsage(
	channel *Channel, accountID, groupID int64,
	platform, model string, tokens UsageTokens, usage accountStatsCostUsage,
) *float64 {
	modelLower := strings.ToLower(model)
	for _, rule := range channel.AccountStatsPricingRules {
		if !matchAccountStatsRule(&rule, accountID, groupID) {
			continue
		}
		pricing := findPricingForModel(rule.Pricing, platform, modelLower)
		if pricing == nil {
			continue // 规则匹配但模型不在规则定价中，继续下一条
		}
		return calculateStatsCostWithUsage(pricing, tokens, usage)
	}
	return nil
}

// matchAccountStatsRule 检查规则是否匹配指定的 accountID 和 groupID。
// 匹配条件：accountID ∈ rule.AccountIDs 或 groupID ∈ rule.GroupIDs。
// 如果规则的 AccountIDs 和 GroupIDs 都为空，视为不匹配。
func matchAccountStatsRule(rule *AccountStatsPricingRule, accountID, groupID int64) bool {
	if len(rule.AccountIDs) == 0 && len(rule.GroupIDs) == 0 {
		return false
	}
	for _, id := range rule.AccountIDs {
		if id == accountID {
			return true
		}
	}
	for _, id := range rule.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// findPricingForModel 在定价列表中查找匹配的模型定价。
// 先精确匹配，再通配符匹配（按配置顺序，先匹配先使用）。
func findPricingForModel(pricingList []ChannelModelPricing, platform, modelLower string) *ChannelModelPricing {
	// 精确匹配优先
	for i := range pricingList {
		p := &pricingList[i]
		if !isPlatformMatch(platform, p.Platform) {
			continue
		}
		for _, m := range p.Models {
			if strings.ToLower(m) == modelLower {
				return p
			}
		}
	}
	// 通配符匹配：按配置顺序，先匹配先使用
	for i := range pricingList {
		p := &pricingList[i]
		if !isPlatformMatch(platform, p.Platform) {
			continue
		}
		for _, m := range p.Models {
			ml := strings.ToLower(m)
			if !strings.HasSuffix(ml, "*") {
				continue
			}
			prefix := strings.TrimSuffix(ml, "*")
			if strings.HasPrefix(modelLower, prefix) {
				return p
			}
		}
	}
	return nil
}

// isPlatformMatch 判断平台是否匹配（空平台视为不限平台）。
func isPlatformMatch(queryPlatform, pricingPlatform string) bool {
	if queryPlatform == "" || pricingPlatform == "" {
		return true
	}
	return queryPlatform == pricingPlatform
}

// calculateStatsCostWithUsage 使用给定的定价计算费用（不含任何倍率，原始费用）。
func calculateStatsCostWithUsage(pricing *ChannelModelPricing, tokens UsageTokens, usage accountStatsCostUsage) *float64 {
	if pricing == nil {
		return nil
	}
	switch pricing.BillingMode {
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		return calculatePerRequestStatsCostWithUsage(pricing, tokens, usage)
	default:
		return calculateTokenStatsCost(pricing, tokens)
	}
}

// calculatePerRequestStatsCostWithUsage 按次/图片/视频计费。
func calculatePerRequestStatsCostWithUsage(
	pricing *ChannelModelPricing,
	tokens UsageTokens,
	usage accountStatsCostUsage,
) *float64 {
	unitPrice := 0.0
	if usage.sizeTier != "" {
		if tier := pricing.GetTierByLabel(usage.sizeTier); tier != nil && tier.PerRequestPrice != nil {
			unitPrice = *tier.PerRequestPrice
		}
	}
	if unitPrice <= 0 {
		totalContext := tokens.InputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
		if iv := FindMatchingInterval(pricing.Intervals, totalContext); iv != nil && iv.PerRequestPrice != nil {
			unitPrice = *iv.PerRequestPrice
		}
	}
	if unitPrice <= 0 && pricing.PerRequestPrice != nil {
		unitPrice = *pricing.PerRequestPrice
	}
	if unitPrice <= 0 {
		return nil
	}
	units := usage.usageUnits
	if pricing.BillingMode != BillingModeVideo || units <= 0 {
		count := usage.requestCount
		if count <= 0 {
			count = 1
		}
		units = float64(count)
	}
	cost := unitPrice * units
	return &cost
}

// calculateTokenStatsCost Token 计费。
// If the pricing has intervals, find the matching interval by total token count
// and use its prices instead of the flat pricing fields.
func calculateTokenStatsCost(pricing *ChannelModelPricing, tokens UsageTokens) *float64 {
	cloned := pricing.Clone()
	p := &cloned
	if len(pricing.Intervals) > 0 {
		totalContext := tokens.InputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
		if iv := FindMatchingInterval(pricing.Intervals, totalContext); iv != nil {
			if iv.InputPrice != nil {
				p.InputPrice = iv.InputPrice
			}
			if iv.OutputPrice != nil {
				p.OutputPrice = iv.OutputPrice
			}
			if iv.CacheWritePrice != nil {
				p.CacheWritePrice = iv.CacheWritePrice
			}
			if iv.CacheReadPrice != nil {
				p.CacheReadPrice = iv.CacheReadPrice
			}
		}
	}
	deref := func(ptr *float64) float64 {
		if ptr == nil {
			return 0
		}
		return *ptr
	}
	inputPrice := deref(p.InputPrice)
	imageInputPrice := inputPrice
	if p.ImageInputPrice != nil && *p.ImageInputPrice > 0 {
		imageInputPrice = *p.ImageInputPrice
	}
	textInputTokens := tokens.InputTokens
	imageInputTokens := 0
	if tokens.ImageInputTokens > 0 {
		imageInputTokens = tokens.ImageInputTokens
		if imageInputTokens > tokens.InputTokens {
			imageInputTokens = tokens.InputTokens
		}
		textInputTokens -= imageInputTokens
	}
	outputPrice := deref(p.OutputPrice)
	imageOutputPrice := outputPrice
	if p.ImageOutputPrice != nil {
		imageOutputPrice = *p.ImageOutputPrice
	}
	textOutputTokens := tokens.OutputTokens
	imageOutputTokens := 0
	if tokens.ImageOutputTokens > 0 {
		imageOutputTokens = tokens.ImageOutputTokens
		if imageOutputTokens > tokens.OutputTokens {
			imageOutputTokens = tokens.OutputTokens
		}
		textOutputTokens -= imageOutputTokens
	}
	cost := float64(textInputTokens)*inputPrice +
		float64(imageInputTokens)*imageInputPrice +
		float64(textOutputTokens)*outputPrice +
		float64(imageOutputTokens)*imageOutputPrice +
		float64(tokens.CacheCreationTokens)*deref(p.CacheWritePrice) +
		float64(tokens.CacheReadTokens)*deref(p.CacheReadPrice)
	if cost <= 0 {
		return nil
	}
	return &cost
}

// applyAccountStatsCost resolves the account stats cost for a usage log entry.
// It resolves the upstream model (falling back to the requested model) and calls
// the 4-level priority chain via resolveAccountStatsCost.
func applyAccountStatsCost(
	ctx context.Context,
	usageLog *UsageLog,
	cs *ChannelService, bs *BillingService,
	accountID int64, groupID int64,
	upstreamModel, requestedModel string,
	tokens UsageTokens,
	totalCost float64,
) {
	if usageLog == nil {
		return
	}
	model := upstreamModel
	if model == "" {
		model = requestedModel
	}
	usage := accountStatsCostUsage{requestCount: 1}
	if usageLog.VideoCount > 0 {
		usage.requestCount = usageLog.VideoCount
		resolution := ""
		if usageLog.VideoResolution != nil {
			resolution = *usageLog.VideoResolution
		}
		usage.sizeTier = NormalizeVideoBillingResolutionOrDefault(resolution)
		duration := 0
		if usageLog.VideoDurationSeconds != nil {
			duration = *usageLog.VideoDurationSeconds
		}
		duration = NormalizeVideoBillingDurationSecondsOrDefault(duration)
		usage.usageUnits = float64(usageLog.VideoCount * duration)
	} else if usageLog.ImageCount > 0 {
		usage.requestCount = usageLog.ImageCount
		imageSize := ""
		if usageLog.ImageSize != nil {
			imageSize = *usageLog.ImageSize
		}
		usage.sizeTier = NormalizeImageBillingTierOrDefault(imageSize)
	}
	serviceTier := ""
	if usageLog.ServiceTier != nil {
		serviceTier = *usageLog.ServiceTier
	}
	usageLog.AccountStatsCost = resolveAccountStatsCostWithUsage(
		ctx, cs, bs, accountID, groupID, model, tokens, usage, totalCost, serviceTier,
	)
}
