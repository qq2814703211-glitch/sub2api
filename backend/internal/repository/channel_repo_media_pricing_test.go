//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const mediaPricingFixtureJSON = `{"extra_input_image_price":0,"free_input_images":0,"duration_source":"output","rules":[{"name":"fixture","unit":"image","conditions":{"resolution":"2K","mode":"standard","input_type":"text","audio":false,"reference_video":true,"layer":false,"min_pixels":0,"max_pixels":4194304,"min_duration":0,"max_duration":10,"fps":24,"min_fps":20,"max_fps":30},"price":0,"prompt_price":0.03}]}`

func mediaPricingRow(value any) *sqlmock.Rows {
	return sqlmock.NewRows(channelModelPricingTimePricingColumns).AddRow(int64(11), int64(7), "openai", `["fixture"]`, service.BillingModeImage, nil, nil, nil, nil, nil, nil, nil, nil, "{}", nil, nil, nil, nil, value, time.Time{}, time.Time{})
}

func TestMediaPricingRepositoryCreateUpdateReplaceRoundTrip(t *testing.T) {
	for _, operation := range []string{"create", "update", "replace"} {
		t.Run(operation, func(t *testing.T) {
			repo, mock := newChannelModelPricingTimePricingRepo(t)
			var media service.MediaPricing
			require.NoError(t, json.Unmarshal([]byte(mediaPricingFixtureJSON), &media))
			pricing := &service.ChannelModelPricing{ID: 11, ChannelID: 7, Platform: "openai", Models: []string{"fixture"}, BillingMode: service.BillingModeImage, MediaPricing: &media}
			stored := &capturedReasoningMultipliersJSON{}
			ctx := context.Background()
			if operation == "update" {
				mock.ExpectExec(`(?s)UPDATE channel_model_pricing.*media_pricing = \$16.*WHERE id = \$18`).WithArgs([]byte(`["fixture"]`), service.BillingModeImage, nil, nil, nil, nil, nil, nil, nil, nil, "{}", nil, nil, nil, nil, stored, "openai", int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
				require.NoError(t, repo.UpdateModelPricing(ctx, pricing))
			} else {
				if operation == "replace" {
					mock.ExpectBegin()
					mock.ExpectExec(`DELETE FROM channel_model_pricing`).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				mock.ExpectQuery(`INSERT INTO channel_model_pricing .*media_pricing`).WithArgs(int64(7), "openai", []byte(`["fixture"]`), service.BillingModeImage, nil, nil, nil, nil, nil, nil, nil, nil, "{}", nil, nil, nil, nil, stored).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), time.Time{}, time.Time{}))
				if operation == "replace" {
					mock.ExpectCommit()
					require.NoError(t, repo.ReplaceModelPricing(ctx, 7, []service.ChannelModelPricing{*pricing}))
				} else {
					require.NoError(t, repo.CreateModelPricing(ctx, pricing))
				}
			}
			require.JSONEq(t, mediaPricingFixtureJSON, stored.value)
			mock.ExpectQuery(`(?s)SELECT .*media_pricing.*FROM channel_model_pricing`).WithArgs(int64(7)).WillReturnRows(mediaPricingRow(stored.value))
			expectEmptyModelPricingIntervals(mock)
			loaded, err := repo.ListModelPricing(ctx, 7)
			require.NoError(t, err)
			require.Equal(t, pricing.MediaPricing, loaded[0].MediaPricing)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestMediaPricingRepositoryBatchAndMalformed(t *testing.T) {
	repo, mock := newChannelModelPricingTimePricingRepo(t)
	mock.ExpectQuery(`(?s)SELECT .*media_pricing.*FROM channel_model_pricing`).WithArgs(sqlmock.AnyArg()).WillReturnRows(mediaPricingRow(mediaPricingFixtureJSON))
	expectEmptyModelPricingIntervals(mock)
	result, err := repo.batchLoadModelPricing(context.Background(), []int64{7})
	require.NoError(t, err)
	require.NotNil(t, result[7][0].MediaPricing.Rules[0].Price)
	require.Zero(t, *result[7][0].MediaPricing.Rules[0].Price)
	require.NoError(t, mock.ExpectationsWereMet())
	for _, value := range []string{`{`, `{"rules":[]}`, `{"rules":[{"unit":"image","conditions":{}}]}`} {
		_, err := unmarshalMediaPricing([]byte(value))
		require.Error(t, err)
	}
	for _, value := range [][]byte{nil, []byte(`null`)} {
		got, err := unmarshalMediaPricing(value)
		require.NoError(t, err)
		require.Nil(t, got)
	}
	got, err := marshalMediaPricing(nil)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestMediaPricingAccountStatsRepositoryRoundTrip(t *testing.T) {
	repo, mock := newChannelModelPricingTimePricingRepo(t)
	ctx := context.Background()
	var media service.MediaPricing
	require.NoError(t, json.Unmarshal([]byte(mediaPricingFixtureJSON), &media))
	pricing := &service.ChannelModelPricing{Platform: "openai", Models: []string{"fixture"}, BillingMode: service.BillingModeImage, MediaPricing: &media}
	stored := &capturedReasoningMultipliersJSON{}
	mock.ExpectBegin()
	tx, err := repo.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`INSERT INTO channel_account_stats_model_pricing .*media_pricing`).WithArgs(int64(9), "openai", []byte(`["fixture"]`), service.BillingModeImage, nil, nil, nil, nil, nil, nil, "{}", nil, nil, stored).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), time.Time{}, time.Time{}))
	require.NoError(t, createAccountStatsModelPricingTx(ctx, tx, 9, pricing))
	mock.ExpectCommit()
	require.NoError(t, tx.Commit())
	require.JSONEq(t, mediaPricingFixtureJSON, stored.value)
	columns := []string{"id", "rule_id", "platform", "models", "billing_mode", "input_price", "output_price", "cache_write_price", "cache_write_1h_price", "cache_write_12h_price", "cache_read_price", "reasoning_effort_multipliers", "image_output_price", "per_request_price", "media_pricing", "created_at", "updated_at"}
	mock.ExpectQuery(`(?s)SELECT .*media_pricing.*FROM channel_account_stats_model_pricing`).WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows(columns).AddRow(int64(11), int64(9), "openai", `["fixture"]`, service.BillingModeImage, nil, nil, nil, nil, nil, nil, "{}", nil, nil, stored.value, time.Time{}, time.Time{}))
	mock.ExpectQuery(`(?s)SELECT .*FROM channel_account_stats_pricing_intervals`).WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	loaded, err := repo.batchLoadAccountStatsModelPricing(ctx, []int64{9})
	require.NoError(t, err)
	require.Equal(t, pricing.MediaPricing, loaded[9][0].MediaPricing)
	require.NoError(t, mock.ExpectationsWereMet())
}
