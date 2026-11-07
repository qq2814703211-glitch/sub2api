//go:build unit

package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercises the real admin HTTP/service/repository chain on an isolated DB.
func TestMediaPricingChannelHTTPPostgresRoundTrip(t *testing.T) {
	dsn := os.Getenv("MEDIA_PRICING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated media pricing PostgreSQL DSN not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, repository.ApplyMigrations(context.Background(), db))
	repo := repository.NewChannelRepository(db)
	channels := service.NewChannelService(repo, nil, nil, nil, nil)
	handler := NewChannelHandler(channels, nil, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/channels", handler.Create)
	router.GET("/channels/:id", handler.GetByID)
	router.PUT("/channels/:id", handler.Update)
	call := func(method, path, payload string, wantStatus int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		require.Equal(t, wantStatus, result.Code, result.Body.String())
		return result.Body.String()
	}
	media := `{"extra_input_image_price":0.02,"free_input_images":1,"duration_source":"output","rules":[{"name":"fixture","unit":"image","conditions":{"layer":false,"audio":false,"reference_video":false,"min_pixels":0,"max_pixels":1048576},"price":0.2,"prompt_price":0.03}]}`
	card := `{"platform":"openai","models":["fixture-image"],"billing_mode":"image","media_pricing":` + media + `}`
	created := call(http.MethodPost, "/channels", `{"name":"media-http-fixture","model_pricing":[`+card+`],"account_stats_pricing_rules":[{"name":"stats","account_ids":[123],"pricing":[`+card+`]}]}`, http.StatusOK)
	id := gjson.Get(created, "data.id").Int()
	require.Positive(t, id)
	t.Cleanup(func() { require.NoError(t, repo.Delete(context.Background(), id)) })
	path := fmt.Sprintf("/channels/%d", id)
	assertConfig := func(body, expected string) {
		t.Helper()
		require.JSONEq(t, expected, gjson.Get(body, "data.model_pricing.0.media_pricing").Raw)
		require.JSONEq(t, media, gjson.Get(body, "data.account_stats_pricing_rules.0.pricing.0.media_pricing").Raw)
	}
	assertConfig(created, media)
	assertConfig(call(http.MethodGet, path, "", http.StatusOK), media)
	assertConfig(call(http.MethodPut, path, `{"description":"retains omitted rules"}`, http.StatusOK), media)
	nextMedia := strings.Replace(media, `"price":0.2`, `"price":0`, 1)
	nextMedia = strings.Replace(nextMedia, `"extra_input_image_price":0.02`, `"extra_input_image_price":0`, 1)
	nextMedia = strings.Replace(nextMedia, `"free_input_images":1`, `"free_input_images":0`, 1)
	nextCard := strings.Replace(card, media, nextMedia, 1)
	assertConfig(call(http.MethodPut, path, `{"model_pricing":[`+nextCard+`]}`, http.StatusOK), nextMedia)
	assertConfig(call(http.MethodGet, path, "", http.StatusOK), nextMedia)
	var stored string
	require.NoError(t, db.QueryRow("SELECT media_pricing::text FROM channel_model_pricing WHERE channel_id=$1", id).Scan(&stored))
	require.JSONEq(t, nextMedia, stored)
	var statsStored string
	require.NoError(t, db.QueryRow(`SELECT p.media_pricing::text FROM channel_account_stats_model_pricing p JOIN channel_account_stats_pricing_rules r ON r.id=p.rule_id WHERE r.channel_id=$1`, id).Scan(&statsStored))
	require.JSONEq(t, media, statsStored)
	for _, bad := range []string{
		strings.Replace(nextCard, `"extra_input_image_price":0`, `"extra_input_image_price":-1`, 1),
		strings.Replace(nextCard, `"free_input_images":0`, `"free_input_images":0.5`, 1),
		strings.Replace(nextCard, `"free_input_images":0`, `"free_input_images":-1`, 1),
		strings.Replace(nextCard, `"duration_source":"output"`, `"duration_source":"input"`, 1),
		strings.Replace(nextCard, `"duration_source":"output"`, `"duration_source":"total"`, 1),
	} {
		call(http.MethodPut, path, `{"model_pricing":[`+bad+`]}`, http.StatusBadRequest)
		assertConfig(call(http.MethodGet, path, "", http.StatusOK), nextMedia)
	}
	invalidCard := strings.Replace(nextCard, `"price":0,`, ``, 1)
	call(http.MethodPut, path, `{"model_pricing":[`+invalidCard+`]}`, http.StatusBadRequest)
	assertConfig(call(http.MethodGet, path, "", http.StatusOK), nextMedia)
	var price service.ChannelModelPricing
	require.NoError(t, json.Unmarshal([]byte(nextCard), &price))
	require.NotNil(t, price.MediaPricing.Rules[0].Price)
	cleared := call(http.MethodPut, path, `{"model_pricing":[{"platform":"openai","models":["fixture-image"],"billing_mode":"image","per_request_price":0,"media_pricing":null}]}`, http.StatusOK)
	require.Equal(t, "null", gjson.Get(cleared, "data.model_pricing.0.media_pricing").Raw)
	var isNull bool
	require.NoError(t, db.QueryRow("SELECT media_pricing IS NULL FROM channel_model_pricing WHERE channel_id=$1", id).Scan(&isNull))
	require.True(t, isNull)
}

func TestMediaPricingGroupHTTPPostgresRoundTrip(t *testing.T) {
	dsn := os.Getenv("MEDIA_PRICING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated media pricing PostgreSQL DSN not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, repository.ApplyMigrations(context.Background(), db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	groups := repository.NewAdminGroupRepository(client, db)
	svc := service.NewAdminService(&config.Config{}, nil, groups, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler := NewGroupHandler(svc, nil, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/groups", handler.Create)
	router.GET("/groups/:id", handler.GetByID)
	router.PUT("/groups/:id", handler.Update)
	call := func(method, path, payload string, wantStatus int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		require.Equal(t, wantStatus, result.Code, result.Body.String())
		return result.Body.String()
	}
	media := `{"extra_input_image_price":0.2,"free_input_images":5,"duration_source":"total","rules":[{"name":"fixture","unit":"second","conditions":{"mode":"pro","audio":false,"reference_video":true},"price":0.4,"prompt_price":0.03}]}`
	card := `{"platform":"openai","models":["fixture-video"],"billing_mode":"video","media_pricing":` + media + `}`
	created := call(http.MethodPost, "/groups", `{"name":"media-http-group-fixture","platform":"openai","rate_multiplier":1,"model_pricing":[`+card+`]}`, http.StatusOK)
	id := gjson.Get(created, "data.id").Int()
	require.Positive(t, id)
	t.Cleanup(func() { require.NoError(t, groups.Delete(context.Background(), id)) })
	path := fmt.Sprintf("/groups/%d", id)
	assertConfig := func(body, expected string) {
		t.Helper()
		require.JSONEq(t, expected, gjson.Get(body, "data.model_pricing.0.media_pricing").Raw)
	}
	assertConfig(created, media)
	assertConfig(call(http.MethodGet, path, "", http.StatusOK), media)
	assertConfig(call(http.MethodPut, path, `{"description":"preserve omitted rules"}`, http.StatusOK), media)
	nextMedia := strings.Replace(media, `"price":0.4`, `"price":0`, 1)
	nextMedia = strings.Replace(nextMedia, `"extra_input_image_price":0.2`, `"extra_input_image_price":0`, 1)
	nextMedia = strings.Replace(nextMedia, `"free_input_images":5`, `"free_input_images":0`, 1)
	nextCard := strings.Replace(card, media, nextMedia, 1)
	assertConfig(call(http.MethodPut, path, `{"model_pricing":[`+nextCard+`]}`, http.StatusOK), nextMedia)
	assertConfig(call(http.MethodGet, path, "", http.StatusOK), nextMedia)
	var raw string
	require.NoError(t, db.QueryRow("SELECT model_pricing->0->'media_pricing' FROM groups WHERE id=$1", id).Scan(&raw))
	require.JSONEq(t, nextMedia, raw)
	for _, bad := range []string{
		strings.Replace(nextCard, `"extra_input_image_price":0`, `"extra_input_image_price":-1`, 1),
		strings.Replace(nextCard, `"free_input_images":0`, `"free_input_images":0.5`, 1),
		strings.Replace(nextCard, `"duration_source":"total"`, `"duration_source":"input"`, 1),
	} {
		call(http.MethodPut, path, `{"model_pricing":[`+bad+`]}`, http.StatusBadRequest)
		assertConfig(call(http.MethodGet, path, "", http.StatusOK), nextMedia)
	}
	invalidCard := strings.Replace(nextCard, `"price":0,`, ``, 1)
	call(http.MethodPut, path, `{"model_pricing":[`+invalidCard+`]}`, http.StatusBadRequest)
	assertConfig(call(http.MethodGet, path, "", http.StatusOK), nextMedia)
	cleared := call(http.MethodPut, path, `{"model_pricing":[{"platform":"openai","models":["fixture-video"],"billing_mode":"video","per_request_price":0,"media_pricing":null}]}`, http.StatusOK)
	require.False(t, gjson.Get(cleared, "data.model_pricing.0.media_pricing").Exists())
}
