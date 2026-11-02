//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Use a baseline-compatible optional seam to prove real gateway observations
// invoke the shared bounded worker, without adding another inference request.
func TestGrokSubscriptionGatewayObservationSchedulesBoundedRefresh(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(7201)
	repo := &subscriptionRefreshRepo{grokQuotaAccountRepo: &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{a.ID: a}}}}
	up := &subscriptionBlockingUpstream{started: make(chan struct{}, 1), release: make(chan struct{}), finished: make(chan struct{}, 1)}
	quota := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), up, nil)
	gateway := &OpenAIGatewayService{}
	setter, ok := any(gateway).(interface{ SetGrokQuotaService(*GrokQuotaService) })
	require.True(t, ok, "gateway must accept the shared subscription refresher")
	setter.SetGrokQuotaService(quota)
	started := time.Now()
	gateway.updateGrokUsageFromResponse(context.Background(), a, http.Header{}, 200)
	require.Less(t, time.Since(started), 100*time.Millisecond, "SSE response observation must not wait for settings")
	select {
	case <-up.started:
	case <-time.After(time.Second):
		t.Fatal("settings never started")
	}
	for i := 0; i < 50; i++ {
		gateway.updateGrokUsageFromResponse(context.Background(), a, http.Header{}, 200)
	}
	close(up.release)
	select {
	case <-up.finished:
	case <-time.After(time.Second):
		t.Fatal("settings did not finish")
	}
	require.Eventually(t, func() bool { return quotaSubscriptionIdle(quota) }, time.Second, time.Millisecond)
	up.mu.Lock()
	require.Equal(t, 1, up.settingsCalls)
	up.mu.Unlock()
}

func TestGrokSubscriptionManualConnectionRefreshesThroughWiring(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(7202)
	a.Credentials["subscription_tier"] = "supergrok_heavy"
	repo := &subscriptionRefreshRepo{grokQuotaAccountRepo: &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{a.ID: a}}}}
	settings := &subscriptionRefreshUpstream{body: `{"subscription_tier_display":"Free"}`}
	provider := NewGrokTokenProvider(repo, nil)
	gateway := &OpenAIGatewayService{}
	ProvideGrokQuotaService(gateway, repo, nil, provider, settings, &config.Config{}, nil, nil)
	inference := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))}}
	svc := &AccountTestService{accountRepo: repo, grokTokenProvider: provider, httpUpstream: inference, openaiGatewayService: gateway}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/test", nil)
	require.NoError(t, svc.testGrokAccountConnection(c, a, "grok-4.5", "", AccountTestModeGrokText, AccountTestOptions{}))
	require.Equal(t, "free", a.GetCredential("subscription_tier"))
	require.Equal(t, 1, settings.settingsCalls)
	require.Zero(t, settings.posts)
}

func TestGrokSubscriptionWorkerDeadlineReleaseAndRegistryBound(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(7203)
	repo := &subscriptionRefreshRepo{grokQuotaAccountRepo: &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{a.ID: a}}}}
	up := &subscriptionDeadlineUpstream{started: make(chan time.Duration, 1), done: make(chan struct{})}
	quota := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), up, nil)
	quota.ScheduleSubscriptionRefresh(a.ID)
	select {
	case duration := <-up.started:
		require.LessOrEqual(t, duration, grokSubscriptionTimeout)
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	for id := int64(8000); id < 10000; id++ {
		quota.ScheduleSubscriptionRefresh(id)
	}
	quota.subscriptionMu.Lock()
	require.LessOrEqual(t, quota.subscriptionWorkers, grokSubscriptionMaxWorkers)
	require.LessOrEqual(t, len(quota.subscriptionTasks), grokSubscriptionMaxEntries)
	quota.subscriptionMu.Unlock()
	select {
	case <-up.done:
	case <-time.After(grokSubscriptionTimeout + time.Second):
		t.Fatal("deadline did not cancel upstream")
	}
	require.Eventually(t, func() bool { return quotaSubscriptionIdle(quota) }, time.Second, time.Millisecond)
	quota.subscriptionMu.Lock()
	quota.subscriptionTasks[a.ID] = grokSubscriptionTask{started: time.Now().Add(-2 * grokSubscriptionRetryInterval)}
	quota.subscriptionMu.Unlock()
	quota.httpUpstream = &subscriptionRefreshUpstream{body: `{"subscription_tier_display":"Free"}`}
	quota.ScheduleSubscriptionRefresh(a.ID)
	require.Eventually(t, func() bool { return quotaSubscriptionIdle(quota) }, time.Second, time.Millisecond)
	require.Equal(t, "free", a.GetCredential("subscription_tier"))
}

type subscriptionDeadlineUpstream struct {
	httpUpstreamRecorder
	started chan time.Duration
	done    chan struct{}
}

func (u *subscriptionDeadlineUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	deadline, ok := req.Context().Deadline()
	if !ok {
		return nil, context.DeadlineExceeded
	}
	u.started <- time.Until(deadline)
	<-req.Context().Done()
	close(u.done)
	return nil, req.Context().Err()
}

// Inspect the worker lifecycle under its lock.
func quotaSubscriptionIdle(s *GrokQuotaService) bool {
	s.subscriptionMu.Lock()
	defer s.subscriptionMu.Unlock()
	return s.subscriptionWorkers == 0
}

type subscriptionBlockingUpstream struct {
	subscriptionRefreshUpstream
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (u *subscriptionBlockingUpstream) Do(req *http.Request, p string, id int64, n int) (*http.Response, error) {
	select {
	case u.started <- struct{}{}:
	default:
	}
	select {
	case <-u.release:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	u.body = `{"subscription_tier_display":"Free"}`
	resp, err := u.subscriptionRefreshUpstream.Do(req, p, id, n)
	select {
	case u.finished <- struct{}{}:
	default:
	}
	return resp, err
}
