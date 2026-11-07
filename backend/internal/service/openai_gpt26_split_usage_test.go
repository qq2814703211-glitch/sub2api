//go:build unit

package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGPT26HostedSplitSnapshotsRetainDriverAndTool(t *testing.T) {
	const tool = `"tool_usage":{"image_gen":{"input_tokens":371,"input_tokens_details":{"image_tokens":352,"text_tokens":19},"output_tokens":439,"output_tokens_details":{"image_tokens":439},"cached_tokens":12,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2}}}`
	for _, ws := range []bool{false, true} {
		t.Run(map[bool]string{false: "sse", true: "ws"}[ws], func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			var usage OpenAIUsage
			parse := func(event string) {
				if ws {
					parseOpenAIWSResponseUsageFromCompletedEvent([]byte(event), &usage)
				} else {
					svc.parseSSEUsage(event, &usage)
				}
			}
			parse(`{"type":"response.in_progress","response":{"usage":{"input_tokens":5,"output_tokens":1,"input_tokens_details":{"cached_tokens":2}},` + tool + `}}`)
			parse(`{"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":9,"input_tokens_details":{"cached_tokens":2}}}}`)
			require.Equal(t, 376, usage.InputTokens, "omitted terminal tool usage must not erase an observed independent component")
			require.Equal(t, 448, usage.OutputTokens)
			require.Equal(t, 352, usage.ImageInputTokens)
			require.Equal(t, 14, usage.CacheReadInputTokens)
			require.Equal(t, 10, usage.ImageCacheReadTokens)
			parse(`{"type":"response.done","response":{` + tool + `}}`)
			require.Equal(t, 376, usage.InputTokens, "tool-only terminal must preserve driver")
			require.Equal(t, 448, usage.OutputTokens)
			parse(`{"type":"response.done","response":{` + tool + `}}`)
			require.Equal(t, 376, usage.InputTokens, "repeated tool-only snapshot is not a delta")
			require.Equal(t, 448, usage.OutputTokens)
		})
	}
}
