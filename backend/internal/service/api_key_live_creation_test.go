//go:build unit

package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"net/http"
	"sync"
	"testing"
	"time"
)

type liveCreationSlots struct {
	apiKeyQueueCacheStub
	muLive                          sync.Mutex
	transfers, migrations, releases int
	joint                           bool
	sources                         map[int64]string
}

func (c *liveCreationSlots) AcquireAccountSlot(_ context.Context, id int64, _ int, r string) (bool, error) {
	c.muLive.Lock()
	defer c.muLive.Unlock()
	c.sources[id] = r
	return true, nil
}
func (c *liveCreationSlots) ReleaseAccountSlot(context.Context, int64, string) error { return nil }
func (c *liveCreationSlots) GetAccountsLoadBatch(_ context.Context, a []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	out := map[int64]*AccountLoadInfo{}
	for _, v := range a {
		out[v.ID] = &AccountLoadInfo{AccountID: v.ID}
	}
	return out, nil
}
func (c *liveCreationSlots) AcquireLiveLease(context.Context, int64, int, int64, int, int64, string, bool) (bool, error) {
	return false, errors.New("must use exact transfer")
}
func (c *liveCreationSlots) AcquireLiveLeaseTransferring(_ context.Context, r LiveLeaseTransferRequest) (bool, error) {
	c.muLive.Lock()
	defer c.muLive.Unlock()
	if r.AccountRequestID == "" || r.AccountRequestID != c.sources[r.AccountID] || r.KeyRequestID == "" {
		return false, ErrLiveLeaseSourceLost
	}
	c.transfers++
	c.joint = true
	return true, nil
}
func (c *liveCreationSlots) MigrateLiveLeaseAccount(_ context.Context, r LiveLeaseTransferRequest) (bool, error) {
	c.muLive.Lock()
	defer c.muLive.Unlock()
	if !c.joint || r.AccountRequestID == "" {
		return false, ErrLiveLeaseSourceLost
	}
	c.migrations++
	return true, nil
}
func (c *liveCreationSlots) ReleaseLiveLeaseAccount(context.Context, int64, string) error { return nil }
func (c *liveCreationSlots) AbortLiveLeaseTransfer(context.Context, LiveLeaseTransferRequest) error {
	return nil
}
func (c *liveCreationSlots) RefreshLiveLease(context.Context, int64, int64, int64, string) (bool, error) {
	return true, nil
}
func (c *liveCreationSlots) ReleaseLiveLease(context.Context, int64, int64, int64, string) error {
	c.muLive.Lock()
	defer c.muLive.Unlock()
	c.releases++
	c.joint = false
	return nil
}

type liveCreationStore struct{ liveTestStore }

func (*liveCreationStore) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, ErrStickySessionNotFound
}
func (*liveCreationStore) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (*liveCreationStore) DeleteSessionAccountID(context.Context, int64, string) error { return nil }

type liveCreationUpstream struct {
	HTTPUpstream
	slots *liveCreationSlots
	calls int
	t     *testing.T
}

func (u *liveCreationUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	u.slots.muLive.Lock()
	held := u.slots.joint
	u.slots.muLive.Unlock()
	require.True(u.t, held, "Live SDP retries retain joint key ownership")
	return nil, errors.New("synthetic upstream failure")
}
func TestAPIKeyProtocolLiveCreationRetainsLeaseAcrossRetriesAndCleansTerminalFailure(t *testing.T) {
	slots := &liveCreationSlots{sources: map[int64]string{}}
	slots.immediate = true
	svc := NewConcurrencyService(slots)
	ctx, cancel := WithAPIKeyAdmissionOwner(context.Background())
	defer cancel()
	key, err := svc.ReserveAPIKeySlotWithWait(ctx, 7, 1)
	require.NoError(t, err)
	defer key.Release()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.JWT.Secret = "synthetic-test-secret"
	accounts := []Account{}
	for i := int64(1); i <= 4; i++ {
		accounts = append(accounts, Account{ID: i, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "synthetic", "chatgpt_account_id": "synthetic"}})
	}
	upstream := &liveCreationUpstream{slots: slots, t: t}
	gateway := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &liveCreationStore{}, concurrencyService: svc, httpUpstream: upstream, liveAttestation: liveAttestationStub{header: `{"v":1}`}, liveAttestationCipher: newLiveAttestationCipher(cfg)}
	_, err = gateway.CreateLiveCall(ctx, &LiveCallRequest{SDP: "v=0", Session: []byte(`{"model":"gpt-live"}`)}, LiveCallIdentity{UserID: 9, APIKeyID: 7, APIKeyConcurrencyLimit: 1, KeyReservation: key}, 0)
	require.Error(t, err)
	require.Greater(t, upstream.calls, 1)
	require.Equal(t, 1, slots.transfers)
	require.Greater(t, slots.migrations, 0)
	require.False(t, slots.joint)
	require.Equal(t, 1, slots.releases)
}
