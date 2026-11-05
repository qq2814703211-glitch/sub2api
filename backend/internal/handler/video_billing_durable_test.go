//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type handlerVideoJobStore struct {
	service.VideoBillingJobRepository
	job                  *service.VideoBillingJob
	createErr, submitErr error
	submitted            string
	onSubmit             func()
}

func (r *handlerVideoJobStore) Create(_ context.Context, j *service.VideoBillingJob) error {
	r.job = j
	return r.createErr
}
func (r *handlerVideoJobStore) Submit(_ context.Context, id, task string) error {
	if r.onSubmit != nil {
		r.onSubmit()
	}
	r.submitted = task
	return r.submitErr
}

func TestDurableVideoHandlerNoReplayOnAmbiguousCreate(t *testing.T) {
	for _, provider := range []string{"grok", "seedance"} {
		t.Run(provider, func(t *testing.T) {
			platform := service.PlatformGrok
			if provider == "seedance" {
				platform = service.PlatformOpenAI
			}
			h, slots, _, u := newGrokMediaSlotHandler(t, false, false, platform)
			// Without persistence configured the compatibility path still must never
			// retry an accepted-or-unknown asynchronous create across any account.
			c, w := grokMediaSlotContext(context.Background(), true)
			u.call = func(*http.Request, int64) (*http.Response, error) {
				return nil, &service.UpstreamFailoverError{StatusCode: 503, RetryableOnSameAccount: true}
			}
			if provider == "seedance" {
				c.Request.Body = io.NopCloser(strings.NewReader(`{"model":"video","content":[{"type":"text","text":"x"}]}`))
				h.handleGrokMedia(c, service.SeedanceEndpointCreate, "")
			} else {
				h.GrokVideoGeneration(c)
			}
			require.Equal(t, http.StatusBadGateway, w.Code)
			require.Equal(t, 1, u.calls)
			slots.assertReleased(t)
		})
	}
}

func TestDurableVideoHandlerPersistenceBeforePublicTask(t *testing.T) {
	for _, scenario := range []string{"save fails", "submit fails", "success"} {
		t.Run(scenario, func(t *testing.T) {
			h, slots, _, u := newGrokMediaSlotHandler(t, false, false)
			c, w := grokMediaSlotContext(context.Background(), true)
			store := &handlerVideoJobStore{}
			if scenario == "save fails" {
				store.createErr = errors.New("synthetic save failure")
			}
			if scenario == "submit fails" {
				store.submitErr = errors.New("synthetic submit failure")
			}
			h.gatewayService.SetVideoBillingJobs(store)
			store.onSubmit = func() { require.Empty(t, w.Body.String()); require.NotNil(t, store.job) }
			h.GrokVideoGeneration(c)
			switch scenario {
			case "save fails":
				require.Equal(t, 503, w.Code)
				require.Zero(t, u.calls)
				require.NotContains(t, w.Body.String(), `"request_id"`)
			case "submit fails":
				require.Equal(t, 502, w.Code)
				require.Equal(t, 1, u.calls)
				require.NotContains(t, w.Body.String(), `"request_id"`)
			case "success":
				require.Equal(t, 200, w.Code)
				require.Equal(t, 1, u.calls)
				require.Equal(t, "task", store.submitted)
			}
			slots.assertReleased(t)
		})
	}
}
