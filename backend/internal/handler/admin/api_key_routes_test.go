package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

type adminRouteTestService struct {
	service.AdminService
	received []int64
}

func (s *adminRouteTestService) AdminUpdateAPIKeyGroupRoutes(_ context.Context, id int64, primary *int64, ids []int64) (*service.AdminUpdateAPIKeyGroupIDResult, error) {
	s.received = append([]int64(nil), ids...)
	return &service.AdminUpdateAPIKeyGroupIDResult{APIKey: &service.APIKey{ID: id, GroupID: &ids[0], RouteGroupIDs: ids}}, nil
}
func TestAdminAPIKeyHandler_OrderedRoutes(t *testing.T) {
	svc := &adminRouteTestService{}
	router := setupAPIKeyHandler(svc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/api-keys/7", bytes.NewBufferString(`{"group_ids":[30,2,1]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	require.Equal(t, []int64{30, 2, 1}, svc.received)
	var response struct {
		Data struct {
			APIKey struct {
				GroupID  int64   `json:"group_id"`
				GroupIDs []int64 `json:"group_ids"`
			} `json:"api_key"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.EqualValues(t, 30, response.Data.APIKey.GroupID)
	require.Equal(t, svc.received, response.Data.APIKey.GroupIDs)
}
