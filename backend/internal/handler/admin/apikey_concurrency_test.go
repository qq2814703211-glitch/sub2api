package admin

import (
	"bytes"
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

type keyConcurrencyAdminStub struct {
	service.AdminService
	limit int
	key   *service.APIKey
}

func (s *keyConcurrencyAdminStub) AdminUpdateAPIKeyGroupID(context.Context, int64, *int64) (*service.AdminUpdateAPIKeyGroupIDResult, error) {
	return &service.AdminUpdateAPIKeyGroupIDResult{APIKey: s.key}, nil
}
func (s *keyConcurrencyAdminStub) AdminSetAPIKeyConcurrency(_ context.Context, _ int64, n int) (*service.APIKey, error) {
	s.limit = n
	s.key.ConcurrencyLimit = n
	return s.key, nil
}
func TestAPIKeyConcurrencyAdminRoundTrip(t *testing.T) {
	s := &keyConcurrencyAdminStub{key: &service.APIKey{ID: 7, ConcurrencyLimit: 3}}
	router := setupAPIKeyHandler(s)
	for _, body := range []string{`{"concurrency_limit":4}`, `{"concurrency_limit":0}`} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/api-keys/7", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code)
		require.Contains(t, rec.Body.String(), "concurrency_limit")
	}
	require.Zero(t, s.limit)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/api-keys/7", bytes.NewBufferString(`{"concurrency_limit":-1}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, 400, rec.Code)
	require.Zero(t, s.limit)
}
