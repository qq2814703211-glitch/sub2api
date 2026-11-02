package service

import (
	"context"
	"strconv"
	"time"
)

const (
	grokSubscriptionTimeout       = 5 * time.Second
	grokSubscriptionRetryInterval = time.Minute
	grokSubscriptionMaxWorkers    = 4
	grokSubscriptionMaxEntries    = 1024
)

type grokSubscriptionTask struct {
	started time.Time
	running bool
}

func (s *OpenAIGatewayService) SetGrokQuotaService(quota *GrokQuotaService) {
	s.grokQuotaService = quota
}

// RefreshSubscription is a non-inference probe shared by manual tests and the
// background observer. Cross-instance ordering is enforced at persistence.
func (s *GrokQuotaService) RefreshSubscription(ctx context.Context, id int64) error {
	if s == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, grokSubscriptionTimeout)
	defer cancel()
	result := s.subscriptionFlight.DoChan(strconv.FormatInt(id, 10), func() (any, error) {
		shared, cancel := context.WithTimeout(context.Background(), grokSubscriptionTimeout)
		defer cancel()
		account, token, proxy, err := s.prepareProbe(shared, id)
		if err != nil {
			return nil, err
		}
		return nil, s.refreshGrokSubscription(shared, account, token, proxy)
	})
	select {
	case <-callCtx.Done():
		return callCtx.Err()
	case r := <-result:
		return r.Err
	}
}

// ScheduleSubscriptionRefresh never waits for network or persistence. Both the
// worker count and per-account retry registry are bounded, with lazy expiry.
func (s *GrokQuotaService) ScheduleSubscriptionRefresh(id int64) {
	if s == nil || id <= 0 {
		return
	}
	now := time.Now()
	s.subscriptionMu.Lock()
	if s.subscriptionTasks == nil {
		s.subscriptionTasks = make(map[int64]grokSubscriptionTask)
	}
	for key, task := range s.subscriptionTasks {
		if !task.running && now.Sub(task.started) >= grokSubscriptionRetryInterval {
			delete(s.subscriptionTasks, key)
		}
	}
	if _, exists := s.subscriptionTasks[id]; exists || s.subscriptionWorkers >= grokSubscriptionMaxWorkers || len(s.subscriptionTasks) >= grokSubscriptionMaxEntries {
		s.subscriptionMu.Unlock()
		return
	}
	s.subscriptionTasks[id] = grokSubscriptionTask{started: now, running: true}
	s.subscriptionWorkers++
	s.subscriptionMu.Unlock()
	go func() {
		defer func() {
			s.subscriptionMu.Lock()
			task := s.subscriptionTasks[id]
			task.running = false
			s.subscriptionTasks[id] = task
			s.subscriptionWorkers--
			s.subscriptionMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), grokSubscriptionTimeout)
		defer cancel()
		account, err := s.loadGrokOAuthAccount(ctx, id)
		if err != nil || !grokSubscriptionNeedsRefresh(account, time.Now()) {
			return
		}
		_ = s.RefreshSubscription(ctx, id)
	}()
}
