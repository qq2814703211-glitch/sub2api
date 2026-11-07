package openai_ws_v2

import (
	"math"
	"strconv"

	"github.com/tidwall/gjson"
)

// Match HTTP Responses: base and hosted tool totals are independent; image_gen
// already aggregates calls. Keep two bounded snapshots, not an event history.
func updateRelayHostedUsage(state *relayState, base Usage, hasBase bool, imageGen gjson.Result, terminal bool) Usage {
	if !state.turnHasImageUsage {
		state.turnBaseUsage = state.turnUsage
	}
	if hasBase {
		if terminal {
			if relayUsageHasTokens(base) || !relayUsageHasTokens(state.turnBaseUsage) {
				state.turnBaseUsage = base
			}
		} else {
			mergeRelayUsageNonZero(&state.turnBaseUsage, base)
		}
	}
	if imageGen.IsObject() {
		state.turnImageUsage = parseRelayHostedImageUsage(imageGen)
		state.turnHasImageUsage = true
	}
	state.turnUsage = sumRelayUsage(state.turnBaseUsage, state.turnImageUsage)
	if terminal {
		return state.turnUsage
	}
	return Usage{}
}

func relayHostedCount(value gjson.Result) int {
	if value.Type != gjson.Number || len(value.Raw) > 64 {
		return 0
	}
	if integer, err := strconv.ParseInt(value.Raw, 10, 64); err == nil {
		if integer < 0 || uint64(integer) > uint64(^uint(0)>>1) {
			return 0
		}
		return int(integer)
	}
	n, err := strconv.ParseFloat(value.Raw, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= float64(int(^uint(0)>>1)) || math.Trunc(n) != n {
		return 0
	}
	return int(n)
}

func parseRelayHostedImageUsage(value gjson.Result) Usage {
	count := func(path string) int { return relayHostedCount(value.Get(path)) }
	input, output := count("input_tokens"), count("output_tokens")
	imageInput := min(input, count("input_tokens_details.image_tokens"))
	imageOutput := min(output, count("output_tokens_details.image_tokens"))
	cache := value.Get("cached_tokens")
	details := value.Get("cached_tokens_details")
	imageCache, textCache := 0, 0
	if details.IsObject() {
		imageCache = count("cached_tokens_details.cached_image_tokens")
		textCache = count("cached_tokens_details.cached_text_tokens")
	} else if !cache.Exists() {
		details = value.Get("input_tokens_details.cached_tokens_details")
		imageCache = count("input_tokens_details.cached_tokens_details.image_tokens")
		textCache = count("input_tokens_details.cached_tokens_details.text_tokens")
	}
	if !cache.Exists() && !value.Get("cached_tokens_details").IsObject() {
		cache = value.Get("input_tokens_details.cached_tokens")
	}
	cached := relayHostedCount(cache)
	if !cache.Exists() && details.IsObject() {
		cached = min(imageCache, input)
		cached += min(textCache, input-cached)
	}
	cached = min(cached, input)
	imageCache = min(imageCache, imageInput, cached)
	return Usage{InputTokens: input, OutputTokens: output, ImageInputTokens: imageInput, ImageOutputTokens: imageOutput, CacheReadInputTokens: cached, ImageCacheReadTokens: imageCache}
}

func sumRelayUsage(a, b Usage) Usage {
	add := func(a, b int) int { a = max(a, 0); b = max(b, 0); return a + min(b, int(^uint(0)>>1)-a) }
	return Usage{InputTokens: add(a.InputTokens, b.InputTokens), OutputTokens: add(a.OutputTokens, b.OutputTokens), ImageInputTokens: add(a.ImageInputTokens, b.ImageInputTokens), ImageOutputTokens: add(a.ImageOutputTokens, b.ImageOutputTokens), CacheReadInputTokens: add(a.CacheReadInputTokens, b.CacheReadInputTokens), ImageCacheReadTokens: add(a.ImageCacheReadTokens, b.ImageCacheReadTokens), CacheCreationInputTokens: add(a.CacheCreationInputTokens, b.CacheCreationInputTokens)}
}
