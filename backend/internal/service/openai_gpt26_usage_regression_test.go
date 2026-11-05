//go:build unit

package service

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func gpt26UsageCounters(usage OpenAIUsage) OpenAIUsage {
	usage.hostedSnapshot = nil
	return usage
}

// Hosted image usage is separate from base usage and already sums image calls.
func TestGPT26HostedUsageSnapshots(t *testing.T) {
	const driver = `"usage":{"input_tokens":5,"output_tokens":9,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":1}}`
	const tool = `"tool_usage":{"image_gen":{"input_tokens":371,"input_tokens_details":{"image_tokens":352,"text_tokens":19},"output_tokens":439,"output_tokens_details":{"image_tokens":439},"cached_tokens":12,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2}}}`
	expected := OpenAIUsage{InputTokens: 376, ImageInputTokens: 352, OutputTokens: 448, ImageOutputTokens: 439, CacheReadInputTokens: 14, ImageCacheReadTokens: 10, CacheCreationInputTokens: 1}
	for _, wrapper := range []string{`{%s}`, `{"response":{%s}}`, `{"data":{%s}}`, `{"data":{"response":{%s}}}`} {
		t.Run(wrapper, func(t *testing.T) {
			usage, ok := extractOpenAIUsageFromJSONBytes([]byte(fmt.Sprintf(wrapper, driver+","+tool)))
			require.True(t, ok)
			require.Equal(t, expected, gpt26UsageCounters(usage))
		})
	}
	t.Run("repeated_progress_and_terminal_snapshots", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		var usage OpenAIUsage
		for _, event := range []string{"response.in_progress", "response.in_progress", "response.completed", "response.completed"} {
			svc.parseSSEUsage(fmt.Sprintf(`{"type":%q,"response":{%s,%s}}`, event, driver, tool), &usage)
			require.Equal(t, expected, gpt26UsageCounters(usage))
		}
		svc.parseSSEUsage(`{"type":"response.done","response":{"id":"r"}}`, &usage)
		require.Equal(t, expected, gpt26UsageCounters(usage))
	})
	t.Run("tool_only", func(t *testing.T) {
		usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{` + tool + `}`))
		require.True(t, ok)
		require.Equal(t, OpenAIUsage{InputTokens: 371, ImageInputTokens: 352, OutputTokens: 439, ImageOutputTokens: 439, CacheReadInputTokens: 12, ImageCacheReadTokens: 10}, gpt26UsageCounters(usage))
	})
	t.Run("base_vision_is_separate_from_tool", func(t *testing.T) {
		usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":100,"input_tokens_details":{"image_tokens":70},"output_tokens":50,"output_tokens_details":{"image_tokens":30}},` + tool + `}`))
		require.True(t, ok)
		require.Equal(t, 471, usage.InputTokens)
		require.Equal(t, 422, usage.ImageInputTokens)
		require.Equal(t, 489, usage.OutputTokens)
		require.Equal(t, 469, usage.ImageOutputTokens)
	})
}

func TestGPT26HostedUsageCachePrecedence(t *testing.T) {
	for _, tt := range []struct {
		name, extra  string
		cache, image int
	}{
		{"unknown", ``, 0, 0},
		{"canonical_details_only", `,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2}`, 12, 10},
		{"canonical_aggregate_zero", `,"cached_tokens":0,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2}`, 0, 0},
		{"aggregate_without_modality", `,"cached_tokens":12`, 12, 0},
		{"canonical_over_alias", `,"cached_tokens":12,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2},"input_tokens_details":{"image_tokens":80,"cached_tokens":50,"cached_tokens_details":{"image_tokens":40,"text_tokens":10}}`, 12, 10},
		{"legacy_nested", `,"input_tokens_details":{"image_tokens":80,"cached_tokens":12,"cached_tokens_details":{"image_tokens":10,"text_tokens":2}}`, 12, 10},
		{"bounded", `,"cached_tokens":1000,"cached_tokens_details":{"cached_image_tokens":1000,"cached_text_tokens":1000}`, 100, 80},
	} {
		t.Run(tt.name, func(t *testing.T) {
			payload := `{"usage":{},"tool_usage":{"image_gen":{"input_tokens":100,"input_tokens_details":{"image_tokens":80,"text_tokens":20},"output_tokens":200,"output_tokens_details":{"image_tokens":200}` + tt.extra + `}}}`
			usage, ok := extractOpenAIUsageFromJSONBytes([]byte(payload))
			require.True(t, ok)
			require.Equal(t, 100, usage.InputTokens)
			require.Equal(t, tt.cache, usage.CacheReadInputTokens)
			require.Equal(t, tt.image, usage.ImageCacheReadTokens)
		})
	}
}
