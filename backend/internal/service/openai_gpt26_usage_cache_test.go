//go:build unit

package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGPT26UsageCanonicalFieldsWinAtZero(t *testing.T) {
	usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":0,"prompt_tokens":99,"output_tokens":0,"completion_tokens":88,"input_tokens_details":{"image_tokens":0,"cached_tokens":0},"prompt_tokens_details":{"image_tokens":77,"cached_tokens":66},"output_tokens_details":{"image_tokens":0},"completion_tokens_details":{"image_tokens":55}}}`))
	require.True(t, ok)
	require.Equal(t, OpenAIUsage{}, usage)
}

func TestGPT26UsageMergePreservesEveryCacheField(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":20,"input_tokens_details":{"image_tokens":10,"cached_tokens":7,"cache_write_tokens":1,"cached_tokens_details":{"image_tokens":4,"text_tokens":3}},"output_tokens":5},"tool_usage":{"image_gen":{"input_tokens":100,"input_tokens_details":{"image_tokens":80,"text_tokens":20},"cached_tokens":12,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2},"output_tokens":200,"output_tokens_details":{"image_tokens":200}}}}`)
	expected := OpenAIUsage{InputTokens: 120, ImageInputTokens: 90, OutputTokens: 205, ImageOutputTokens: 200, CacheReadInputTokens: 19, CacheCreationInputTokens: 1, ImageCacheReadTokens: 14}
	var usage OpenAIUsage
	mergeOpenAIUsage(&usage, body)
	require.Equal(t, expected, gpt26UsageCounters(usage))
	mergeOpenAIUsage(&usage, body)
	require.Equal(t, expected, gpt26UsageCounters(usage))
}

func TestGPT26HostedToolOnlySSEAndWS(t *testing.T) {
	event := []byte(`{"type":"response.completed","response":{"tool_usage":{"image_gen":{"input_tokens":100,"input_tokens_details":{"image_tokens":80,"text_tokens":20},"output_tokens":200,"output_tokens_details":{"image_tokens":200}}}}}`)
	expected := OpenAIUsage{InputTokens: 100, ImageInputTokens: 80, OutputTokens: 200, ImageOutputTokens: 200}
	var sse, ws OpenAIUsage
	(&OpenAIGatewayService{}).parseSSEUsageBytes(event, &sse)
	require.Equal(t, expected, gpt26UsageCounters(sse))
	require.True(t, openAIWSMessageShouldParseUsage("response.completed", event))
	parseOpenAIWSResponseUsageFromCompletedEvent(event, &ws)
	require.Equal(t, expected, gpt26UsageCounters(ws))
}
