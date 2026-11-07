package openai_ws_v2

import (
	"context"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestRelayGPT26HostedIndependentUsage(t *testing.T) {
	const fields = `"input_tokens":371,"input_tokens_details":{"image_tokens":352,"text_tokens":19},"output_tokens":439,"output_tokens_details":{"image_tokens":439},"cached_tokens":12,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2}`
	first := `{"type":"response.in_progress","response":{"id":"r_image","usage":{"input_tokens":5,"output_tokens":1,"input_tokens_details":{"cached_tokens":2}},"tool_usage":{"image_gen":{` + fields + `}}}}`
	terminal := `{"type":"response.completed","response":{"id":"r_image","usage":{"input_tokens":5,"output_tokens":9,"input_tokens_details":{"cached_tokens":2}}}}`
	client := newPassthroughTestFrameConn(nil, false)
	upstream := newPassthroughTestFrameConn([]passthroughTestFrame{
		{msgType: coderws.MessageText, payload: []byte(first)},
		{msgType: coderws.MessageText, payload: []byte(first)},
		{msgType: coderws.MessageText, payload: []byte(terminal)},
		{msgType: coderws.MessageText, payload: []byte(terminal)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"r_text","usage":{"input_tokens":3,"output_tokens":4}}}`)},
	}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var turns []RelayTurnResult
	result, exit := Relay(ctx, client, upstream, []byte(`{"type":"response.create","model":"gpt-5.6-luna","input":[]}`), RelayOptions{OnTurnComplete: func(turn RelayTurnResult) { turns = append(turns, turn) }})
	require.Nil(t, exit)
	require.Len(t, turns, 2, "duplicate terminal is one request, not a second billable turn")
	require.Equal(t, 376, turns[0].Usage.InputTokens)
	require.Equal(t, 448, turns[0].Usage.OutputTokens)
	require.Equal(t, 352, turns[0].Usage.ImageInputTokens)
	require.Equal(t, 14, turns[0].Usage.CacheReadInputTokens)
	require.Equal(t, 10, turns[0].Usage.ImageCacheReadTokens)
	require.Equal(t, 10, result.Usage.ImageCacheReadTokens)
	require.Equal(t, Usage{InputTokens: 3, OutputTokens: 4}, turns[1].Usage)
	require.Equal(t, 379, result.Usage.InputTokens)
	require.Equal(t, 452, result.Usage.OutputTokens)
}
