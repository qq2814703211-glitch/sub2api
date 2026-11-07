package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// OpenAI 缓存写入按 TTL 分档计价（与 Anthropic 的 5 分钟 / 1 小时 ephemeral 缓存是
// 两套语义，见 ADR 0011）。官方口径：
//
//   - 缓存读：输入价 × 0.1（GPT-6.1 Sol / Terra 为 × 0.05），两种 TTL 同价；
//   - 30 分钟缓存写入：输入价 × 1.25（默认 TTL，不指定 prompt_cache_options 时使用）；
//   - 12 小时缓存写入：输入价 × 2.5（Responses API 的 prompt_cache_options.ttl = "12h"）。
//
// 写入价是**总价**，不是在输入价之上加收；30 分钟升 12 小时会重新计一次 12 小时写入，
// 不抵扣。因此这里只按本次请求上报的分档 token 计价，不读历史、不做抵扣。
const (
	// OpenAICacheWriteTTL30m 是默认缓存写入档（30 分钟）。
	OpenAICacheWriteTTL30m = "30m"
	// OpenAICacheWriteTTL12h 是 12 小时缓存写入档。
	OpenAICacheWriteTTL12h = "12h"

	// openAICacheWrite30mMultiplier 30 分钟缓存写入倍率（相对输入价）。
	openAICacheWrite30mMultiplier = 1.25
	// openAICacheWrite12hMultiplier 12 小时缓存写入倍率（相对输入价）。
	openAICacheWrite12hMultiplier = 2.5
)

// openAICacheWriteTTLFromPromptCacheOptions 从请求体的 prompt_cache_options.ttl 取 TTL 档位。
// 未提供、空串或非法值时返回 30m（官方默认）。返回值只用于日志/展示口径，
// 计费始终以上游 usage 上报的分档 token 为准，不用请求意图臆造写入量。
func openAICacheWriteTTLFromPromptCacheOptions(body []byte) string {
	if len(body) == 0 {
		return OpenAICacheWriteTTL30m
	}
	ttl := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_options.ttl").String()))
	switch ttl {
	case "12h", "12hr", "12hour", "12hours":
		return OpenAICacheWriteTTL12h
	default:
		return OpenAICacheWriteTTL30m
	}
}

// openAICacheWritePrice 返回某档缓存写入的每 token 单价：
// 优先使用显式配置（ModelPricing.CacheWrite30mPrice / CacheWrite12hPrice），
// 未配置时按官方倍率从输入价推导。inputPrice 必须是本次请求实际生效的输入价
// （已含 priority / 长上下文调整），否则推导出的写入价会与结算口径不一致。
func openAICacheWritePrice(pricing *ModelPricing, ttl string, inputPrice float64) float64 {
	if pricing != nil {
		if ttl == OpenAICacheWriteTTL12h && pricing.CacheWrite12hPrice > 0 {
			return pricing.CacheWrite12hPrice
		}
		if ttl != OpenAICacheWriteTTL12h && pricing.CacheWrite30mPrice > 0 {
			return pricing.CacheWrite30mPrice
		}
	}
	if inputPrice <= 0 {
		return 0
	}
	if ttl == OpenAICacheWriteTTL12h {
		return inputPrice * openAICacheWrite12hMultiplier
	}
	return inputPrice * openAICacheWrite30mMultiplier
}

// splitOpenAICacheWriteTTLTokens 把 OpenAI 30m / 12h 分档 token 从缓存写入总量里切出来，
// 返回一个 CacheCreationTokens 只剩「未分档余量」的副本，以及两个分档的 token 数。
//
// 分档是缓存写入总量的子集：上游同时给总量与分档时，余量仍按旧口径
// （CacheCreationPricePerToken）计价；只给分档时总量口径由分档之和定义。分档之和
// 超过总量时按 30m 优先裁剪，保证结算不高于上游上报的写入总量。
// Anthropic 路径不进这两个字段，函数对它是 no-op（返回原值、两档为 0）。
func splitOpenAICacheWriteTTLTokens(tokens UsageTokens) (UsageTokens, int, int) {
	if tokens.CacheWrite30mTokens <= 0 && tokens.CacheWrite12hTokens <= 0 {
		return tokens, 0, 0
	}
	cacheWrite30m := max(tokens.CacheWrite30mTokens, 0)
	cacheWrite12h := max(tokens.CacheWrite12hTokens, 0)
	total := max(tokens.CacheCreationTokens, 0)
	if sum := cacheWrite30m + cacheWrite12h; sum > total {
		if cacheWrite30m >= total {
			cacheWrite30m = total
			cacheWrite12h = 0
		} else {
			cacheWrite12h = total - cacheWrite30m
		}
	}
	cloned := tokens
	cloned.CacheCreationTokens = max(total-cacheWrite30m-cacheWrite12h, 0)
	// 分档 token 已在 cloned 里以独立字段保留，clear 它们避免下游再次看到重复口径。
	cloned.CacheWrite30mTokens = 0
	cloned.CacheWrite12hTokens = 0
	if cloned.CacheCreationTokens == 0 {
		// 全部写入都已分档：Anthropic 5m/1h 明细也必须清零，否则
		// computeCacheCreationCost 的分档分支会用 5m/1h 单价再算一次。
		cloned.CacheCreation5mTokens = 0
		cloned.CacheCreation1hTokens = 0
	}
	return cloned, cacheWrite30m, cacheWrite12h
}

// computeOpenAICacheWriteTTLCost 计算 OpenAI 30 分钟 / 12 小时缓存写入费用。
// inputPrice 是本次请求实际生效的输入价（含 priority 档），inputMultiplier 用于
// 长上下文等输入侧价格缩放（普通调用传 1.0）。Anthropic 的 5m / 1h 路径不经过这里。
func (s *BillingService) computeOpenAICacheWriteTTLCost(pricing *ModelPricing, cacheWrite30m, cacheWrite12h int, inputPrice, inputMultiplier float64) float64 {
	if cacheWrite30m <= 0 && cacheWrite12h <= 0 {
		return 0
	}
	if inputMultiplier <= 0 {
		inputMultiplier = 1
	}
	scaledInputPrice := inputPrice * inputMultiplier
	cost := float64(cacheWrite30m) * openAICacheWritePrice(pricing, OpenAICacheWriteTTL30m, scaledInputPrice)
	cost += float64(cacheWrite12h) * openAICacheWritePrice(pricing, OpenAICacheWriteTTL12h, scaledInputPrice)
	return cost
}

// openAICacheWriteTTLTokensFromUsage 从上游 usage 对象读取缓存写入的 TTL 分档。
// 上游不返回分档时返回 (0, 0)：此时只按 cached_tokens 计缓存读，绝不臆造写入分档
// （ADR 0011 的降级约定）。分档字段既可能是 input_tokens_details 下的嵌套字段，
// 也可能是顶层字段。
func openAICacheWriteTTLTokensFromUsage(value gjson.Result) (cacheWrite30m, cacheWrite12h int) {
	cacheWrite30m = firstPositiveGJSONInt(
		value.Get("input_tokens_details.cache_write_30m_tokens"),
		value.Get("prompt_tokens_details.cache_write_30m_tokens"),
		value.Get("cache_write_30m_tokens"),
	)
	cacheWrite12h = firstPositiveGJSONInt(
		value.Get("input_tokens_details.cache_write_12h_tokens"),
		value.Get("prompt_tokens_details.cache_write_12h_tokens"),
		value.Get("cache_write_12h_tokens"),
	)
	return cacheWrite30m, cacheWrite12h
}
