package service

// openAIHostedUsageSnapshot keeps independent response components across SSE/WS
// snapshots. It is immutable after publication, contains no payloads and is
// copied with OpenAIUsage. Components never contain another snapshot.
type openAIHostedUsageSnapshot struct {
	base, image       OpenAIUsage
	hasBase, hasImage bool
}

func sumOpenAIUsageComponents(base, image OpenAIUsage) OpenAIUsage {
	add := func(a, b int) int {
		a = max(a, 0)
		b = max(b, 0)
		return a + min(b, int(^uint(0)>>1)-a)
	}
	return OpenAIUsage{
		InputTokens:              add(base.InputTokens, image.InputTokens),
		ImageInputTokens:         add(base.ImageInputTokens, image.ImageInputTokens),
		ImageCacheReadTokens:     add(base.ImageCacheReadTokens, image.ImageCacheReadTokens),
		OutputTokens:             add(base.OutputTokens, image.OutputTokens),
		CacheCreationInputTokens: add(base.CacheCreationInputTokens, image.CacheCreationInputTokens),
		CacheReadInputTokens:     add(base.CacheReadInputTokens, image.CacheReadInputTokens),
		ImageOutputTokens:        add(base.ImageOutputTokens, image.ImageOutputTokens),
	}
}

func openAIUsageComponents(usage OpenAIUsage) openAIHostedUsageSnapshot {
	if usage.hostedSnapshot != nil {
		return *usage.hostedSnapshot
	}
	return openAIHostedUsageSnapshot{base: usage, hasBase: true}
}

// mergeOpenAIHostedUsage replaces each available component, not the combined
// total. A missing tool component is unknown, not zero. A present aggregate,
// including zero, is authoritative and is never added to its previous value.
func mergeOpenAIHostedUsage(dst *OpenAIUsage, src OpenAIUsage, terminal bool) bool {
	if dst == nil || (dst.hostedSnapshot == nil && src.hostedSnapshot == nil) {
		return false
	}
	previous, next := openAIUsageComponents(*dst), openAIUsageComponents(src)
	if next.hasBase {
		if terminal {
			if openAIUsageHasTokens(&next.base) || !openAIUsageHasTokens(&previous.base) {
				previous.base = next.base
			}
		} else {
			mergeOpenAIUsageNonZero(&previous.base, next.base)
		}
		previous.hasBase = true
	}
	if next.hasImage {
		previous.image = next.image
		previous.hasImage = true
	}
	combined := sumOpenAIUsageComponents(previous.base, previous.image)
	combined.hostedSnapshot = &previous
	*dst = combined
	return true
}

func replaceOpenAITerminalUsage(dst *OpenAIUsage, src OpenAIUsage) {
	if dst == nil || mergeOpenAIHostedUsage(dst, src, true) {
		return
	}
	if !openAIUsageHasTokens(&src) && openAIUsageHasTokens(dst) {
		return
	}
	*dst = src
}
