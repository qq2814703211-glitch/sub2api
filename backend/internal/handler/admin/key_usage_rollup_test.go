//go:build unit

package admin

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/keyusage"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestKeyUsageAdminRuntimeLifecycle(t *testing.T) {
	dsn := os.Getenv("KEY_USAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated KEY_USAGE_TEST_DSN required")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("key_api_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`) })
	if strings.HasPrefix(dsn, "postgres") {
		u, e := url.Parse(dsn)
		require.NoError(t, e)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE settings(key text PRIMARY KEY,value text NOT NULL,updated_at timestamptz DEFAULT now());CREATE TABLE usage_logs(id bigserial PRIMARY KEY,api_key_id bigint NOT NULL,created_at timestamptz NOT NULL,actual_cost numeric NOT NULL)`)
	require.NoError(t, err)
	runtime := keyusage.New(db)
	defer runtime.Stop()
	h := NewSettingHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetKeyUsageRuntime(runtime)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/runtime", h.GetKeyUsageRollup)
	router.PUT("/runtime", h.UpdateKeyUsageRollup)
	router.DELETE("/runtime", h.PurgeKeyUsageRollup)
	call := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/runtime", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, http.StatusBadRequest, call("PUT", `{}`).Code)
	require.Equal(t, http.StatusBadRequest, call("PUT", `{"enabled":true,"interval_seconds":-1}`).Code)
	require.Equal(t, http.StatusOK, call("PUT", `{"enabled":true,"interval_seconds":3600}`).Code)
	var raw string
	require.NoError(t, db.QueryRow(`SELECT value FROM settings WHERE key=$1`, keyusage.SettingKey).Scan(&raw))
	var config keyusage.Config
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	require.True(t, config.Enabled)
	require.Equal(t, http.StatusOK, call("GET", "").Code)
	require.Equal(t, http.StatusOK, call("PUT", `{"enabled":false}`).Code)
	require.NoError(t, db.QueryRow(`SELECT value FROM settings WHERE key=$1`, keyusage.SettingKey).Scan(&raw))
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	require.False(t, config.Enabled)
	require.Equal(t, http.StatusOK, call("DELETE", "").Code)
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM settings WHERE key=$1`, keyusage.SettingKey).Scan(&n))
	require.Zero(t, n)
}
