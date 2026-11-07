//go:build unit

package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT26HostedSnapshotOwnershipAndBounds(t *testing.T) {
	svc := &OpenAIGatewayService{}
	var usage OpenAIUsage
	svc.parseSSEUsage(`{"type":"response.in_progress","response":{"usage":{"input_tokens":5,"output_tokens":9},"tool_usage":{"image_gen":{"input_tokens":100,"input_tokens_details":{"image_tokens":80},"output_tokens":200,"output_tokens_details":{"image_tokens":200},"cached_tokens":12,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2}}}}}`, &usage)
	captured := usage
	snapshot := *captured.hostedSnapshot
	svc.parseSSEUsage(`{"type":"response.completed","response":{"tool_usage":{"image_gen":{"input_tokens":0,"output_tokens":0,"cached_tokens":0}}}}`, &usage)
	require.Equal(t, 5, usage.InputTokens)
	require.Equal(t, 9, usage.OutputTokens)
	require.Zero(t, usage.ImageInputTokens)
	require.Zero(t, usage.CacheReadInputTokens)
	require.Equal(t, snapshot, *captured.hostedSnapshot, "published snapshot must remain immutable")
	require.Equal(t, 105, captured.InputTokens)
	encoded, err := json.Marshal(captured)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "hostedSnapshot")
	require.NotContains(t, string(encoded), "hasBase")
	require.Nil(t, captured.hostedSnapshot.base.hostedSnapshot)
	require.Nil(t, captured.hostedSnapshot.image.hostedSnapshot)

	maxInt := int(^uint(0) >> 1)
	for _, raw := range []string{`-1`, `1.2`, `"100"`, `1e99999999999999999999999999`} {
		t.Run(raw, func(t *testing.T) {
			parsed, ok := extractOpenAIUsageFromJSONBytes([]byte(fmt.Sprintf(`{"usage":{"input_tokens":5,"output_tokens":9},"tool_usage":{"image_gen":{"input_tokens":%s,"output_tokens":%s,"input_tokens_details":{"image_tokens":999},"output_tokens_details":{"image_tokens":999}}}}`, raw, raw)))
			require.True(t, ok)
			require.Equal(t, 5, parsed.InputTokens)
			require.Equal(t, 9, parsed.OutputTokens)
			require.Zero(t, parsed.ImageInputTokens)
			require.Zero(t, parsed.ImageOutputTokens)
		})
	}
	parsed, ok := extractOpenAIUsageFromJSONBytes([]byte(fmt.Sprintf(`{"usage":{"input_tokens":5,"output_tokens":9},"tool_usage":{"image_gen":{"input_tokens":%d,"output_tokens":%d}}}`, maxInt, maxInt)))
	require.True(t, ok)
	require.Equal(t, maxInt, parsed.InputTokens)
	require.Equal(t, maxInt, parsed.OutputTokens)
	require.Zero(t, parsed.ImageOutputTokens, "omitted output classification is not inferred")
}

func TestGPT26HostedUsageDoesNotSumDuplicateRepresentations(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":5,"output_tokens":9},"tool_usage":{"image_gen":{"input_tokens":371,"input_tokens_details":{"image_tokens":352},"output_tokens":439,"output_tokens_details":{"image_tokens":439}}},"output":[{"type":"image_generation_call","id":"same_image","usage":{"input_tokens":371,"output_tokens":439}},{"type":"image_generation_call","id":"same_image","usage":{"input_tokens":371,"output_tokens":439}}],"data":{"usage":{"input_tokens":500,"output_tokens":900},"tool_usage":{"image_gen":{"input_tokens":371,"output_tokens":439}}}}`)
	usage, ok := extractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 376, usage.InputTokens)
	require.Equal(t, 448, usage.OutputTokens)
	require.Equal(t, 352, usage.ImageInputTokens)
	require.Equal(t, 439, usage.ImageOutputTokens)
}
