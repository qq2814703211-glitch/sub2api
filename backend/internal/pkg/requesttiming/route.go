package requesttiming

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
)

// RouteObservation measures a gateway boundary, not model latency. In
// particular, websocket_write ends at Write, not at the response terminal.
type RouteObservation struct {
	Route     transportdiag.Route `json:"route"`
	AccountID int64               `json:"account_id"`
	Phase     string              `json:"phase"`
	StartMS   float64             `json:"start_ms"`
	EndMS     *float64            `json:"end_ms,omitempty"`
	Status    int                 `json:"status,omitempty"`
	Error     string              `json:"error,omitempty"`
}

func StartRoute(ctx context.Context, accountID int64, phase string, route transportdiag.Route) func(int, error) {
	c := From(ctx)
	if c == nil {
		return func(int, error) {}
	}
	c.mu.Lock()
	if c.finished || len(c.data.Routes) >= 32 {
		c.data.Truncated = true
		c.mu.Unlock()
		return func(int, error) {}
	}
	i := len(c.data.Routes)
	c.data.Routes = append(c.data.Routes, RouteObservation{Route: route, AccountID: accountID, Phase: phase, StartMS: c.offset(time.Now())})
	c.mu.Unlock()
	return func(status int, err error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finished || c.data.Routes[i].EndMS != nil {
			return
		}
		v := c.offset(time.Now())
		a := &c.data.Routes[i]
		a.EndMS, a.Status = &v, status
		if err != nil {
			a.Error = errorKind(err)
		}
	}
}

// AddWSAttempt records one bounded, metadata-only logical WebSocket turn.
func (c *Collector) AddWSAttempt(attempt Attempt) func(status int, terminal string, err error) {
	if c == nil {
		return func(int, string, error) {}
	}
	c.mu.Lock()
	if c.finished || len(c.data.Attempts) >= 32 {
		c.data.Truncated = true
		c.mu.Unlock()
		return func(int, string, error) {}
	}
	attempt.Number = len(c.data.Attempts) + 1
	attempt.StartMS = c.offset(time.Now())
	if attempt.Events == nil {
		attempt.Events = map[string]float64{}
	}
	idx := len(c.data.Attempts)
	c.data.Attempts = append(c.data.Attempts, attempt)
	c.mu.Unlock()
	var once sync.Once
	return func(status int, terminal string, err error) {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.finished {
				return
			}
			a := &c.data.Attempts[idx]
			a.Status, a.Terminal = status, terminal
			v := c.offset(time.Now())
			a.EndMS = &v
			if terminal != "" {
				a.Events["terminal"] = v
			}
			if err != nil {
				a.Error = errorKind(err)
			}
			switch terminal {
			case "failed", "response.failed", "error", "response.cancelled", "response.canceled":
				c.data.Outcome = "failed"
				c.data.Terminal = "failed"
			case "incomplete", "response.incomplete":
				c.data.Terminal = "incomplete"
			case "completed", "response.completed", "response.done":
				c.data.Terminal = "completed"
			}
		})
	}
}

// StartWSAttempt is deliberately independent of the body-capture package.
func StartWSAttempt(ctx context.Context, accountID int64, route transportdiag.Route, kind string) func(status int, terminal string, err error) {
	c := From(ctx)
	if c == nil {
		return func(int, string, error) {}
	}
	return c.AddWSAttempt(Attempt{Route: route, AccountID: accountID, Kind: kind})
}

func (c *Collector) SetWSTurn(turn int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.finished {
		c.data.Protocol = "websocket"
		c.data.Turn = turn
	}
}

// SetWSRoute updates policy metadata after ingress mode selection.
func (c *Collector) SetWSRoute(mode, upstream string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	for i := range c.data.Attempts {
		if c.data.Attempts[i].Kind == "websocket_turn" {
			c.data.Attempts[i].Route.Mode = mode
			c.data.Attempts[i].Route.Upstream = upstream
		}
	}
}

// WSFrame counts payload bytes and fixed event codes; it never retains frames.
func (c *Collector) WSFrame(request bool, size int, semantic bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	for i := range c.data.Attempts {
		a := &c.data.Attempts[i]
		if a.Kind != "websocket_turn" {
			continue
		}
		if request {
			a.RequestBytes += int64(size)
		} else {
			a.ResponseBytes += int64(size)
			if size > 0 {
				if _, ok := a.Events["first_byte"]; !ok {
					a.Events["first_byte"] = c.offset(time.Now())
				}
			}
			if semantic {
				if _, ok := a.Events["first_semantic"]; !ok {
					a.Events["first_semantic"] = c.offset(time.Now())
				}
			}
		}
		if err != nil {
			a.Error = errorKind(err)
		}
	}
}

// SetRoute annotates the confirmed dispatch boundary, e.g. a plugin owns the
// request and its internal network transport cannot be observed by the host.
func SetRoute(ctx context.Context, route transportdiag.Route) {
	if ctx == nil {
		return
	}
	if t, ok := ctx.Value(attemptKey{}).(*Trace); ok {
		t.update(func(a *Attempt) { a.Route = route })
	}
}
