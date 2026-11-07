package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestoutcome"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type wsDiagnosticsKey struct{}

func (s *OpenAIGatewayService) StopConnectionDiagnostics() {
	if repo, ok := s.usageLogRepo.(interface{ StopConnectionDiagnostics() }); ok {
		repo.StopConnectionDiagnostics()
	}
}

type wsConnectionDiagnostics struct {
	mu            sync.Mutex
	base          ConnectionTrace
	record        func(ConnectionTrace)
	enabled       func() bool
	current       *requesttiming.Collector
	model         string
	mode          string
	turn          int
	finishAttempt func(int, string, error)
	awaitDelivery bool
	delivered     bool
	pending       bool
	terminal      string
	turnErr       error
}

func boundedConnectionMetadata(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	if len(value) > limit {
		value = strings.ToValidUTF8(value[:limit], "")
	}
	return value
}

func (s *OpenAIGatewayService) beginWSConnectionDiagnostics(ctx context.Context, c *gin.Context, account *Account, first []byte, hooks *OpenAIWSIngressHooks) (context.Context, *OpenAIWSIngressHooks, func(error)) {
	if requesttiming.From(ctx) == nil {
		return ctx, hooks, func(error) {}
	}
	repo, ok := s.usageLogRepo.(interface {
		RecordConnectionTrace(ConnectionTrace)
		ConnectionDiagnosticsEnabled() bool
	})
	if !ok || !repo.ConnectionDiagnosticsEnabled() {
		return ctx, hooks, func(error) {}
	}
	base := ConnectionTrace{Source: "application", Method: "GET", Path: ConnectionPath(c.Request.URL.Path), Host: boundedConnectionMetadata(c.Request.Host, 255), HTTPVersion: c.Request.Proto, Scheme: "ws", Stream: true, AccountID: account.ID}
	if c.Request.TLS != nil {
		base.Scheme = "wss"
		base.TLSVersion = tls.VersionName(c.Request.TLS.Version)
	}
	base.RequestID, _ = c.Request.Context().Value(ctxkey.RequestID).(string)
	base.ClientRequestID, _ = c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	if value, exists := c.Get("api_key"); exists {
		if key, ok := value.(*APIKey); ok && key != nil {
			base.UserID = key.UserID
			base.APIKeyID = key.ID
			if key.GroupID != nil {
				base.GroupID = *key.GroupID
			}
		}
	}
	d := &wsConnectionDiagnostics{base: base, record: repo.RecordConnectionTrace, enabled: repo.ConnectionDiagnosticsEnabled, mode: "ctx_pool"}
	ctx = requesttiming.WithSource(context.WithValue(ctx, wsDiagnosticsKey{}, d), d)
	wrapped := OpenAIWSIngressHooks{}
	if hooks != nil {
		wrapped = *hooks
	}
	if wrapped.ClientLifecycleContext != nil {
		wrapped.ClientLifecycleContext = requesttiming.WithSource(context.WithValue(wrapped.ClientLifecycleContext, wsDiagnosticsKey{}, d), d)
	}
	original := hooks
	wrapped.BeforeRequest = func(turn int, payload []byte, model string) error {
		d.begin(turn, model)
		if original != nil && original.BeforeRequest != nil {
			return original.BeforeRequest(turn, payload, model)
		}
		return nil
	}
	wrapped.AfterTurn = func(turn int, result *OpenAIForwardResult, err error) {
		d.end(result, err)
		if original != nil && original.AfterTurn != nil {
			original.AfterTurn(turn, result, err)
		}
	}
	model := ""
	if hooks != nil {
		model = hooks.InitialRequestModel
	}
	if model == "" {
		model = openAIWSPassthroughRequestModelForFrame(first)
	}
	d.begin(1, model)
	return ctx, &wrapped, func(err error) { d.close(err) }
}

func (d *wsConnectionDiagnostics) Collector() *requesttiming.Collector {
	if d == nil || !d.enabled() {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.current
}
func wsDiagnostics(ctx context.Context) *wsConnectionDiagnostics {
	if ctx == nil {
		return nil
	}
	d, _ := ctx.Value(wsDiagnosticsKey{}).(*wsConnectionDiagnostics)
	return d
}
func (d *wsConnectionDiagnostics) begin(turn int, model string) {
	if !d.enabled() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current != nil {
		return
	}
	d.turn = turn
	d.model = boundedConnectionMetadata(model, 256)
	d.current = requesttiming.New(time.Now(), -1)
	d.current.SetWSTurn(turn)
	d.pending = false
	d.delivered = false
	d.terminal = ""
	d.turnErr = nil
	d.finishAttempt = d.current.AddWSAttempt(requesttiming.Attempt{AccountID: d.base.AccountID, Kind: "websocket_turn", Route: transportdiag.Route{Downstream: "websocket", Upstream: d.upstream(), Mode: d.mode, Endpoint: "responses"}})
}
func (d *wsConnectionDiagnostics) upstream() string {
	if d.mode == "http_bridge" {
		return "http"
	}
	return "websocket"
}
func (d *wsConnectionDiagnostics) setMode(mode string, awaitDelivery bool) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mode = mode
	d.awaitDelivery = awaitDelivery
	if d.current != nil {
		d.current.SetWSRoute(mode, d.upstream())
	}
}
func diagnosticWSTerminal(value string) string {
	switch value {
	case "completed", "response.completed", "response.done":
		return "completed"
	case "response.incomplete", "incomplete":
		return "incomplete"
	case "failed", "error", "response.failed", "response.cancelled", "response.canceled":
		return "failed"
	}
	return ""
}
func (d *wsConnectionDiagnostics) end(result *OpenAIForwardResult, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current == nil {
		return
	}
	d.pending = true
	if err != nil {
		d.turnErr = err
	}
	if result != nil {
		d.terminal = diagnosticWSTerminal(result.UpstreamTerminalEvent)
	}
	if err != nil && d.terminal == "" {
		d.terminal = "failed"
	}
	if !d.awaitDelivery || d.delivered || err != nil {
		d.finishLocked()
	}
}
func (d *wsConnectionDiagnostics) finishLocked() {
	col := d.current
	if col == nil {
		return
	}
	d.current = nil
	terminal := d.terminal
	if terminal == "" {
		terminal = "incomplete"
	}
	ctx := requesttiming.With(context.Background(), col)
	requesttiming.Output(ctx, false, false, terminal)
	if errors.Is(d.turnErr, context.Canceled) || coderws.CloseStatus(d.turnErr) == coderws.StatusNormalClosure || coderws.CloseStatus(d.turnErr) == coderws.StatusGoingAway {
		requesttiming.Outcome(ctx, "client_disconnected", true)
	} else if d.turnErr != nil {
		requesttiming.Outcome(ctx, "failed", false)
	}
	d.finishAttempt(http.StatusSwitchingProtocols, terminal, d.turnErr)
	v := d.base
	v.Model = d.model
	col.WhenFinished(func(detail requesttiming.Snapshot) {
		if v.APIKeyID > 0 {
			detail.IdentitySource = "authenticated"
		}
		v.Detail = detail
		v.TraceID = detail.TraceID
		v.StartedAt = detail.StartedAt
		d.record(v)
	})
	col.Finish(http.StatusSwitchingProtocols, errors.Is(d.turnErr, context.Canceled))
}
func (d *wsConnectionDiagnostics) close(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current == nil {
		return
	}
	d.turnErr = err
	if err == nil && d.terminal == "" {
		d.terminal = "incomplete"
	}
	d.finishLocked()
}

// The original writer owns bytes/cancellation; observation never retries writes.
func writeDiagnosticWSClient(ctx context.Context, conn interface {
	Write(context.Context, coderws.MessageType, []byte) error
}, kind coderws.MessageType, payload []byte) error {
	start := time.Now()
	err := conn.Write(ctx, kind, payload)
	d := wsDiagnostics(ctx)
	if d == nil {
		return err
	}
	col := d.Collector()
	if col == nil {
		return err
	}
	n := 0
	if err == nil {
		n = len(payload)
	}
	col.Written(start, n, err, err == nil)
	if err != nil {
		d.mu.Lock()
		if d.current == col {
			d.turnErr = err
		}
		d.mu.Unlock()
	}
	if kind == coderws.MessageText {
		observer := requestoutcome.NewOutcomeObserver("application/json")
		observer.Write(payload)
		terminal := observer.Finish()
		if protocolTerminal := diagnosticWSTerminal(gjson.GetBytes(payload, "type").String()); protocolTerminal != "" {
			terminal = protocolTerminal
		}
		// Generic JSON frames are not successful WS turn terminals.
		if terminal == "completed" && !openAIWSPassthroughIsTerminalOutput(payload) {
			terminal = ""
		}
		if terminal != "" {
			d.mu.Lock()
			if d.current == col {
				d.terminal = terminal
				d.delivered = true
				if err != nil {
					d.turnErr = err
				}
				if err == nil {
					requesttiming.TerminalFlushed(requesttiming.With(ctx, col), terminal)
				}
				if d.awaitDelivery && d.pending {
					d.finishLocked()
				}
			}
			d.mu.Unlock()
		}
	}
	return err
}
