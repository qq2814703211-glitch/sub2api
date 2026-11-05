//go:build unit

package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyConcurrencyBindingRoundTrip(t *testing.T) {
	for _, raw := range []string{`{"name":"test","concurrency_limit":0}`, `{"name":"test","concurrency_limit":7}`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(raw))
		c.Request.Header.Set("Content-Type", "application/json")
		var req CreateAPIKeyRequest
		require.NoError(t, c.ShouldBindJSON(&req))
	}
	for _, raw := range []string{`{"name":"test","concurrency_limit":1.5}`, `{"name":"test","concurrency_limit":2147483648}`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(raw))
		c.Request.Header.Set("Content-Type", "application/json")
		var req CreateAPIKeyRequest
		require.Error(t, c.ShouldBindJSON(&req))
	}
}
func TestAPIKeyConcurrencyNegativeRejected(t *testing.T) {
	for _, update := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/api/v1/keys", strings.NewReader(`{"name":"queue-test","concurrency_limit":-1}`))
		c.Request.Header.Set("Content-Type", "application/json")
		var err error
		if update {
			var req UpdateAPIKeyRequest
			err = c.ShouldBindJSON(&req)
		} else {
			var req CreateAPIKeyRequest
			err = c.ShouldBindJSON(&req)
		}
		require.Error(t, err, "negative key concurrency must be rejected at the API boundary")
	}
}
