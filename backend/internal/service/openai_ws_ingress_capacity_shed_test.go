package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// openAIWSIngressCapacityShedRepo 补齐 SetError，避免非容量类错误（如
// workspace_suspended）走到账号状态副作用时打空指针。
type openAIWSIngressCapacityShedRepo struct {
	stubOpenAIAccountRepo
}

func (r *openAIWSIngressCapacityShedRepo) SetError(context.Context, int64, string) error { return nil }

func (r *openAIWSIngressCapacityShedRepo) SetRateLimited(context.Context, int64, time.Time) error {
	return nil
}

func (r *openAIWSIngressCapacityShedRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}

// ctx_pool 的 ingress 直写路径把 error / response.failed 交给 WS 客户端前，必须和
// HTTP/SSE（openai_gateway_response_handling.go）与 http_bridge
// （openai_ws_http_bridge.go）两条路径一样，把容量降载码改写为可重试的
// server_error：Codex 按闭集判定，server_is_overloaded / slow_down 属致命集，
// 客户端会打印 "Selected model is at capacity" 并直接终止会话而不是退避重试。
//
// 改写只发生在已经写出过下游帧的轮次：首个输出前的容量降载由服务端回退
// （见 FailsOverCapacityShedBeforeOutput），不会出现在客户端帧里。
//
// 第二个用例锁住改写范围：非容量类错误码必须原样下发，客户端依赖原码各自处理。
func TestProxyResponsesWebSocketFromClient_RewritesCapacityShedCodeForClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		upstreamEvents [][]byte
		wantContains   []string
		wantAbsent     []string
	}{
		{
			name: "capacity_shed_after_output_is_rewritten",
			upstreamEvents: [][]byte{
				[]byte(`{"type":"response.created","response":{"id":"resp_shed"}}`),
				[]byte(`{"type":"response.output_text.delta","delta":"partial"}`),
				[]byte(`{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}`),
				[]byte(`{"type":"response.failed","response":{"id":"resp_shed","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`),
			},
			wantContains: []string{
				`"code":"server_error"`,
				"Our servers are currently overloaded",
			},
			wantAbsent: []string{"server_is_overloaded"},
		},
		{
			name: "non_capacity_error_code_is_passed_through",
			upstreamEvents: [][]byte{
				[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"workspace_suspended","message":"workspace is suspended"}}`),
				[]byte(`{"type":"response.failed","response":{"id":"resp_suspended","status":"failed","error":{"code":"workspace_suspended","message":"workspace is suspended"}}}`),
			},
			wantContains: []string{
				`"code":"workspace_suspended"`,
				"workspace is suspended",
			},
			wantAbsent: []string{"server_error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

			events := make([][]byte, 0, len(tt.upstreamEvents))
			for _, event := range tt.upstreamEvents {
				events = append(events, append([]byte(nil), event...))
			}
			captureConn := &openAIWSCaptureConn{events: events}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})

			account := Account{
				ID:          5401,
				Name:        "openai-ingress-capacity-shed",
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true},
			}
			repo := &openAIWSIngressCapacityShedRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}}}
			svc := &OpenAIGatewayService{
				accountRepo:      repo,
				rateLimitService: &RateLimitService{accountRepo: repo},
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				cfg:              cfg,
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}

			serverDone := make(chan struct{})
			serverErrCh := make(chan error, 1)
			wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(serverDone)
				conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
				if err != nil {
					return
				}
				defer func() { _ = conn.CloseNow() }()

				rec := httptest.NewRecorder()
				ginCtx, _ := gin.CreateTestContext(rec)
				req := r.Clone(r.Context())
				req.Header = req.Header.Clone()
				req.Header.Set("User-Agent", "unit-test-agent/1.0")
				ginCtx.Request = req

				readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				msgType, firstMessage, readErr := conn.Read(readCtx)
				cancel()
				if readErr != nil || (msgType != coderws.MessageText && msgType != coderws.MessageBinary) {
					return
				}
				serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, &account, "sk-test", firstMessage, nil)
			}))
			defer wsServer.Close()

			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
			cancelDial()
			require.NoError(t, err)
			defer func() { _ = clientConn.CloseNow() }()

			writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
			err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
			cancelWrite()
			require.NoError(t, err)

			var frames []string
			for len(frames) < len(tt.upstreamEvents) {
				readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, message, readErr := clientConn.Read(readCtx)
				cancel()
				if readErr != nil {
					break
				}
				frames = append(frames, string(message))
			}
			// 本轮已终止，主动断开客户端让 ingress 退出 turn 循环。
			_ = clientConn.CloseNow()

			require.NotEmpty(t, frames, "客户端应至少收到一个下发事件")
			joined := strings.Join(frames, "\n")
			for _, want := range tt.wantContains {
				require.Contains(t, joined, want, "客户端收到的事件:\n%s", joined)
			}
			for _, absent := range tt.wantAbsent {
				require.NotContains(t, joined, absent, "客户端收到的事件:\n%s", joined)
			}

			select {
			case <-serverDone:
			case <-time.After(5 * time.Second):
				t.Fatal("等待 ingress websocket 结束超时")
			}
			require.NoError(t, <-serverErrCh)
		})
	}
}

type capacityShedIngressHarness struct {
	account   *Account
	client    *coderws.Conn
	serverErr chan error
	afterTurn chan *OpenAIForwardResult
}

// newCapacityShedIngressHarness 启动一个真实的 client<->ingress websocket 服务，
// upstream 由 openAIWSCaptureConn 提供。lifecycle 传给 hooks.ClientLifecycleContext，
// 用于模拟客户端生命周期取消（下游写出会随该 context 取消而失败）。
func newCapacityShedIngressHarness(t *testing.T, events [][]byte, readDelays []time.Duration, lifecycle context.Context) *capacityShedIngressHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	clonedEvents := make([][]byte, 0, len(events))
	for _, event := range events {
		clonedEvents = append(clonedEvents, append([]byte(nil), event...))
	}
	captureConn := &openAIWSCaptureConn{events: clonedEvents, readDelays: readDelays}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})

	account := &Account{
		ID:          5403,
		Name:        "openai-ingress-capacity-shed-failover",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}
	repo := &openAIWSIngressCapacityShedRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
	svc := &OpenAIGatewayService{
		accountRepo:      repo,
		rateLimitService: &RateLimitService{accountRepo: repo},
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		cfg:              cfg,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}

	h := &capacityShedIngressHarness{
		account:   account,
		serverErr: make(chan error, 1),
		afterTurn: make(chan *OpenAIForwardResult, 1),
	}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			h.serverErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", "unit-test-agent/1.0")
		ginCtx.Request = req

		readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, readErr := conn.Read(readCtx)
		cancel()
		if readErr != nil {
			h.serverErr <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			h.serverErr <- fmt.Errorf("unexpected client message type: %s", msgType.String())
			return
		}
		hooks := &OpenAIWSIngressHooks{
			ClientLifecycleContext: lifecycle,
			AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
				if result == nil {
					return
				}
				select {
				case h.afterTurn <- result:
				default:
				}
			},
		}
		h.serverErr <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, hooks)
	}))
	t.Cleanup(wsServer.Close)

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientConn.CloseNow() })
	h.client = clientConn

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
	cancelWrite()
	require.NoError(t, err)
	return h
}

func (h *capacityShedIngressHarness) readFrames(t *testing.T, count int) []string {
	t.Helper()
	frames := make([]string, 0, count)
	for len(frames) < count {
		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, message, readErr := h.client.Read(readCtx)
		cancel()
		if readErr != nil {
			break
		}
		frames = append(frames, string(message))
	}
	return frames
}

// 首个下游输出前的容量降载必须交回服务端重选：返回 503 请求级瞬时错误，
// 允许同账号重试且不把降载记到账号头上；原始 payload 保留未改写，客户端
// 一个帧都不应收到。
func TestProxyResponsesWebSocketFromClient_CapacityShedFailsOverBeforeOutput(t *testing.T) {
	capacityPayload := `{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}`
	h := newCapacityShedIngressHarness(t, [][]byte{[]byte(capacityPayload)}, nil, nil)

	select {
	case err := <-h.serverErr:
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
		require.True(t, failoverErr.RetryableOnSameAccount)
		require.True(t, failoverErr.RequestScopedTransient)
		require.Equal(t, http.StatusServiceUnavailable, failoverErr.ClientStatusCode)
		require.Contains(t, failoverErr.ClientMessage, "currently overloaded")
		// 服务端判定必须基于未改写的原始 payload。
		require.Contains(t, string(failoverErr.ResponseBody), "server_is_overloaded")
		require.NotContains(t, string(failoverErr.ResponseBody), `"code":"server_error"`)
	case <-time.After(5 * time.Second):
		t.Fatal("等待输出前容量降载回退超时")
	}

	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, message, readErr := h.client.Read(readCtx)
	cancel()
	require.Error(t, readErr, "输出前降载不应向客户端写出任何帧: %s", message)
}

// 非容量错误在输出前仍按原合同透传给客户端：不做 failover，也不改写错误码。
func TestProxyResponsesWebSocketFromClient_NonCapacityErrorBeforeOutputPassesThrough(t *testing.T) {
	h := newCapacityShedIngressHarness(t, [][]byte{
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"workspace_suspended","message":"workspace is suspended"}}`),
		[]byte(`{"type":"response.failed","response":{"id":"resp_suspended","status":"failed","error":{"code":"workspace_suspended","message":"workspace is suspended"}}}`),
	}, nil, nil)

	frames := h.readFrames(t, 2)
	_ = h.client.CloseNow()
	require.Len(t, frames, 2)
	joined := strings.Join(frames, "\n")
	require.Contains(t, joined, `"code":"workspace_suspended"`)
	require.NotContains(t, joined, "server_error")

	select {
	case err := <-h.serverErr:
		var failoverErr *UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr), "非容量错误不应触发 failover: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 ingress 结束超时")
	}
}

// 客户端在输出后取消/断连时，容量降载不得改写为服务端重试：ingress 继续排空
// 上游以拿到终态 usage，且不返回 failover 错误触发跨账号重放。
func TestProxyResponsesWebSocketFromClient_ClientCancelAfterOutputDrainsCapacityShed(t *testing.T) {
	lifecycle, cancelLifecycle := context.WithCancel(context.Background())
	defer cancelLifecycle()

	h := newCapacityShedIngressHarness(t, [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_cancel_shed"}}`),
		[]byte(`{"type":"response.output_text.delta","delta":"partial"}`),
		[]byte(`{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_cancel_shed","status":"completed","usage":{"input_tokens":11,"output_tokens":7}}}`),
	}, []time.Duration{0, 0, 200 * time.Millisecond, 0}, lifecycle)

	frames := h.readFrames(t, 2)
	require.Len(t, frames, 2, "取消前应已收到两个输出帧")
	require.Contains(t, frames[1], "partial")

	// 模拟客户端生命周期取消：后续下游写出失败，但上游必须继续排空。
	cancelLifecycle()

	select {
	case result := <-h.afterTurn:
		require.NotNil(t, result)
		require.Equal(t, "resp_cancel_shed", result.RequestID)
		require.Equal(t, 11, result.Usage.InputTokens)
		require.Equal(t, 7, result.Usage.OutputTokens)
	case <-time.After(5 * time.Second):
		t.Fatal("客户端取消后 ingress 未排空到终态 usage")
	}

	_ = h.client.CloseNow()
	select {
	case err := <-h.serverErr:
		var failoverErr *UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr), "客户端已取消时不应返回 failover 错误: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 ingress 结束超时")
	}
}

func TestProxyResponsesWebSocketFromClient_MarksCyberPolicyBeforeEarlyReturn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		upstreamEvent []byte
		wantFailover  bool
		wantClientMsg bool
		wantInput     int
		wantOutput    int
	}{
		{
			name:          "error_before_rate_limit_failover",
			upstreamEvent: []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"cyber_policy","message":"rate limit exceeded by cyber policy"},"usage":{"input_tokens":5,"output_tokens":1}}`),
			wantFailover:  true,
			wantInput:     5,
			wantOutput:    1,
		},
		{
			name:          "response_failed_terminal",
			upstreamEvent: []byte(`{"type":"response.failed","response":{"id":"resp_cyber","status":"failed","error":{"code":"cyber_policy","message":"blocked by cyber policy"},"usage":{"input_tokens":9,"output_tokens":2}}}`),
			wantClientMsg: true,
			wantInput:     9,
			wantOutput:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0

			captureConn := &openAIWSCaptureConn{events: [][]byte{append([]byte(nil), tt.upstreamEvent...)}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID: 5402, Name: "openai-ingress-cyber", Platform: PlatformOpenAI,
				Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool,
				},
			}

			markCh := make(chan *CyberPolicyMark, 1)
			serverErrCh := make(chan error, 1)
			wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
				if err != nil {
					serverErrCh <- err
					return
				}
				defer func() { _ = conn.CloseNow() }()

				readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
				_, firstMessage, err := conn.Read(readCtx)
				cancelRead()
				if err != nil {
					serverErrCh <- err
					return
				}

				recorder := httptest.NewRecorder()
				ginCtx, _ := gin.CreateTestContext(recorder)
				ginCtx.Request = r.Clone(r.Context())
				hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, _ *OpenAIForwardResult, _ error) {
					markCh <- GetOpsCyberPolicy(ginCtx)
				}}
				serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, hooks)
			}))
			defer wsServer.Close()

			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
			cancelDial()
			require.NoError(t, err)
			defer func() { _ = clientConn.CloseNow() }()

			writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
			err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
			cancelWrite()
			require.NoError(t, err)

			if tt.wantClientMsg {
				readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
				_, message, readErr := clientConn.Read(readCtx)
				cancelRead()
				require.NoError(t, readErr)
				require.Equal(t, "response.failed", gjson.GetBytes(message, "type").String())
			}

			select {
			case mark := <-markCh:
				require.NotNil(t, mark)
				require.Equal(t, "cyber_policy", mark.Code)
				require.Equal(t, tt.wantInput, mark.UpstreamInTok)
				require.Equal(t, tt.wantOutput, mark.UpstreamOutTok)
			case <-time.After(3 * time.Second):
				t.Fatal("AfterTurn did not observe the cyber mark")
			}

			_ = clientConn.CloseNow()
			select {
			case serverErr := <-serverErrCh:
				var failoverErr *UpstreamFailoverError
				require.Equal(t, tt.wantFailover, errors.As(serverErr, &failoverErr))
			case <-time.After(5 * time.Second):
				t.Fatal("waiting for ingress websocket shutdown timed out")
			}
		})
	}
}
