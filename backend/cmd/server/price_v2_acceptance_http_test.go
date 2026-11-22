//go:build pricev2audit

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// The external runner observes the generated injector's existing objects. The
// public server retains its original routes and middleware. The second listener
// is reachable only from this disposable container's loopback via docker exec.
var auditHandler *admin.ModelPricingSyncHandler
var auditSync *service.ModelPricingSyncService

func auditObserve(h *admin.ModelPricingSyncHandler, s *service.ModelPricingSyncService) {
	auditHandler, auditSync = h, s
}

type auditRemote struct{}

func (auditRemote) FetchPricingJSON(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://upstream:8090/source?url="+url.QueryEscape(target), nil)
	if err != nil {
		return nil, err
	}
	c := http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("offline fixture source: HTTP %d", r.StatusCode)
	}
	return io.ReadAll(io.LimitReader(r.Body, 4<<20))
}
func (a auditRemote) FetchHashText(ctx context.Context, target string) (string, error) {
	b, e := a.FetchPricingJSON(ctx, target)
	return string(b), e
}

func TestPriceV2AcceptanceHTTPProcess(t *testing.T) {
	// Do not initialize repositories, use ambient config, or open sockets unless
	// invoked by the runner inside its own named network, with a private config.
	if os.Getenv("PRICE_V2_AUDIT_CHILD") != "1" {
		t.Fatal("use receipts/audit-harness/run.py; refusing ambient configuration")
	}
	if os.Getenv("DATABASE_HOST") != "pg" || os.Getenv("REDIS_HOST") != "redis" || os.Getenv("DATA_DIR") != "/fixture/state" {
		t.Fatal("unexpected isolation coordinates")
	}
	a, err := initializeApplication(handler.BuildInfo{Version: "audit", BuildType: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	if auditHandler == nil || auditSync == nil {
		t.Fatal("test observer not installed")
	}
	g := gin.New()
	const p = "/api/v1/admin/model-pricing-sync"
	g.GET(p+"/status", auditHandler.Status)
	g.GET(p+"/cny-official", auditHandler.GetCNYOfficialConfig)
	g.PUT(p+"/cny-official", auditHandler.UpdateCNYOfficialConfig)
	g.POST("/fixture/sync", func(c *gin.Context) {
		_, err := auditSync.Sync(c.Request.Context())
		if err != nil {
			c.JSON(503, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
	go func() { _ = http.ListenAndServe("127.0.0.1:8081", g) }()
	if err := a.Server.ListenAndServe(); err != nil {
		t.Fatal(err)
	}
}

// A separate invocation is a loopback HTTP client, not a handler invocation.
// Read request bodies from stdin; credentials do not appear in process args.
func TestPriceV2AcceptanceClient(t *testing.T) {
	if os.Getenv("PRICE_V2_AUDIT_CHILD") != "1" {
		t.Fatal("only inside audit container")
	}
	var in struct {
		Method, Path, Key string
		Admin             bool
		Body              json.RawMessage
	}
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		t.Fatal(err)
	}
	port := "8080"
	if in.Admin {
		port = "8081"
	}
	req, err := http.NewRequest(in.Method, "http://127.0.0.1:"+port+in.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Body) > 0 && string(in.Body) != "null" {
		req.Body = io.NopCloser(bytes.NewReader(in.Body))
		req.ContentLength = int64(len(in.Body))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if in.Key != "" {
		req.Header.Set("Authorization", "Bearer "+in.Key)
		req.Header.Set("x-api-key", in.Key)
	}
	c := http.Client{Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	var body any
	if json.Unmarshal(b, &body) != nil {
		body = string(b)
	}
	out, _ := json.Marshal(map[string]any{"status": r.StatusCode, "body": body})
	fmt.Println("AUDIT_HTTP=" + string(out))
}
