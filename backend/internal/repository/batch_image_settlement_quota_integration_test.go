//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 批图结算的 Key 配额验收走真实仓储 + 真实 PostgreSQL，并调用真正的
// BatchImageSettlementService.Settle（与 releaseqa run-10 的同一形态：
// 持久化 job → hold → settle），而不是只测一条 SQL 片段。

type batchImageQuotaUnitPrice float64

func (p batchImageQuotaUnitPrice) BatchImageUnitPrice(_ context.Context, _ *service.BatchImageJob) (float64, error) {
	return float64(p), nil
}

type batchImageQuotaOwner struct {
	userID    int64
	apiKeyID  int64
	accountID int64
}

func newBatchImageQuotaOwner(t *testing.T, balance, quota, quotaUsed float64) batchImageQuotaOwner {
	t.Helper()
	client := testEntClient(t)
	suffix := uuid.NewString()
	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("batch-image-quota-%s@example.com", suffix),
		PasswordHash: "hash",
		Balance:      balance,
	})
	key := mustCreateApiKey(t, client, &service.APIKey{
		UserID:    user.ID,
		Key:       "sk-batch-image-quota-" + suffix,
		Name:      "batch-image-quota",
		Quota:     quota,
		QuotaUsed: quotaUsed,
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "batch-image-quota-account-" + suffix,
		Type: service.AccountTypeAPIKey,
	})
	return batchImageQuotaOwner{userID: user.ID, apiKeyID: key.ID, accountID: account.ID}
}

type batchImageQuotaJobOptions struct {
	itemCount       int
	successCount    int
	failCount       int
	hold            float64
	unitPrice       float64
	snapshotVersion int
	mode            service.BillingMode
	successImages   []int
	failedItems     int
}

func newBatchImageQuotaJob(t *testing.T, repo service.BatchImageRepository, owner batchImageQuotaOwner, opts batchImageQuotaJobOptions) *service.BatchImageJob {
	t.Helper()
	ctx := context.Background()
	hold := opts.hold
	batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	requestHash := "request-" + batchID
	job, err := repo.CreateBatchImageJob(ctx, service.CreateBatchImageJobParams{
		BatchID:                batchID,
		UserID:                 owner.userID,
		APIKeyID:               &owner.apiKeyID,
		AccountID:              &owner.accountID,
		Provider:               service.BatchImageProviderVertex,
		Model:                  "gemini-2.5-flash-image",
		Status:                 service.BatchImageJobStatusSettling,
		ItemCount:              opts.itemCount,
		SuccessCount:           opts.successCount,
		FailCount:              opts.failCount,
		EstimatedCost:          hold,
		HoldAmount:             &hold,
		PricingSnapshotVersion: opts.snapshotVersion,
		BillableUnitPrice:      opts.unitPrice,
		CatalogUnitMode:        opts.mode,
		RequestHash:            &requestHash,
	})
	require.NoError(t, err)
	for i, images := range opts.successImages {
		_, err := repo.CreateBatchImageItem(ctx, service.CreateBatchImageItemParams{
			JobID:      batchID,
			CustomID:   fmt.Sprintf("ok-%d", i),
			Status:     service.BatchImageParsedStatusSucceeded,
			ImageCount: images,
		})
		require.NoError(t, err)
	}
	for i := 0; i < opts.failedItems; i++ {
		_, err := repo.CreateBatchImageItem(ctx, service.CreateBatchImageItemParams{
			JobID:    batchID,
			CustomID: fmt.Sprintf("failed-%d", i),
			Status:   "failed",
		})
		require.NoError(t, err)
	}
	return job
}

func reserveBatchImageQuotaHold(t *testing.T, billing service.UsageBillingRepository, job *service.BatchImageJob, owner batchImageQuotaOwner) {
	t.Helper()
	hold := job.EstimatedCost
	if job.HoldAmount != nil {
		hold = *job.HoldAmount
	}
	_, err := billing.ReserveBatchImageBalance(context.Background(), &service.BatchImageBalanceHoldCommand{
		RequestID:          service.BatchImageHoldRequestID(job.BatchID),
		APIKeyID:           owner.apiKeyID,
		UserID:             owner.userID,
		BatchID:            job.BatchID,
		HoldAmount:         hold,
		RequestPayloadHash: batchImageQuotaDeref(job.RequestHash),
	})
	require.NoError(t, err)
}

func newBatchImageQuotaSettlement(t *testing.T, repo service.BatchImageRepository, billing service.UsageBillingRepository, unitPrice float64) *service.BatchImageSettlementService {
	t.Helper()
	return &service.BatchImageSettlementService{
		Repo:         repo,
		BillingRepo:  billing,
		UsageLogRepo: NewUsageLogRepository(testEntClient(t), integrationDB),
		Pricing:      batchImageQuotaUnitPrice(unitPrice),
	}
}

func batchImageQuotaDeref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func readBatchImageQuotaWallet(t *testing.T, userID int64) (balance, frozen float64) {
	t.Helper()
	require.NoError(t, integrationDB.QueryRow(
		`SELECT balance, COALESCE(frozen_balance, 0) FROM users WHERE id = $1`, userID,
	).Scan(&balance, &frozen))
	return balance, frozen
}

func readBatchImageQuotaKey(t *testing.T, apiKeyID int64) (quotaUsed float64, status string) {
	t.Helper()
	require.NoError(t, integrationDB.QueryRow(
		`SELECT quota_used, status FROM api_keys WHERE id = $1`, apiKeyID,
	).Scan(&quotaUsed, &status))
	return quotaUsed, status
}

func countBatchImageQuotaDedup(t *testing.T, requestIDs ...string) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRow(
		`SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = ANY($1)`, pq.Array(requestIDs),
	).Scan(&count))
	return count
}

func readBatchImageQuotaUsageLog(t *testing.T, requestID string) (actualCost float64, units sql.NullFloat64, imageCount int, billingMode sql.NullString) {
	t.Helper()
	require.NoError(t, integrationDB.QueryRow(
		`SELECT actual_cost, billing_units, image_count, billing_mode FROM usage_logs WHERE request_id = $1`, requestID,
	).Scan(&actualCost, &units, &imageCount, &billingMode))
	return actualCost, units, imageCount, billingMode
}

func TestBatchImageSettlementQuotaMatchesWalletOnPartialSuccess(t *testing.T) {
	ctx := context.Background()
	owner := newBatchImageQuotaOwner(t, 100, 100, 1.36)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 2, successCount: 1, failCount: 1,
		hold: 1, unitPrice: 0.375, snapshotVersion: 1, mode: service.BillingModePerImage,
		successImages: []int{2}, failedItems: 1,
	})
	reserveBatchImageQuotaHold(t, billing, job, owner)

	beforeBalance, beforeFrozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99, beforeBalance, 0.00000001)
	require.InDelta(t, 1, beforeFrozen, 0.00000001)
	quotaBefore, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 1.36, quotaBefore, 0.00000001)

	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)
	result, err := svc.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	require.False(t, result.AlreadySettled)
	require.InDelta(t, 0.75, result.ActualCost, 0.00000001)

	balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99.25, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	// 反例：修复前钱包总额减 0.75 而 Key 配额停在 1.36。
	quotaUsed, status := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 2.11, quotaUsed, 0.00000001)
	require.Equal(t, service.StatusAPIKeyActive, status)

	settled, err := repo.GetBatchImageJobByBatchID(ctx, job.BatchID)
	require.NoError(t, err)
	require.Equal(t, service.BatchImageJobStatusCompleted, settled.Status)
	require.NotNil(t, settled.ActualCost)
	require.InDelta(t, 0.75, *settled.ActualCost, 0.00000001)
	require.NotEmpty(t, batchImageQuotaDeref(settled.ManifestHash))

	captureRequestID := service.BatchImageCaptureRequestID(job.BatchID)
	actualCost, units, imageCount, billingMode := readBatchImageQuotaUsageLog(t, captureRequestID)
	require.InDelta(t, 0.75, actualCost, 0.00000001)
	require.True(t, units.Valid)
	require.InDelta(t, 2, units.Float64, 0.00000001)
	require.Equal(t, 2, imageCount)
	require.True(t, billingMode.Valid)
	require.Equal(t, string(service.BillingModePerImage), billingMode.String)

	require.Equal(t, 2, countBatchImageQuotaDedup(t,
		service.BatchImageHoldRequestID(job.BatchID), captureRequestID))
	t.Logf("observed batch=%s wallet_before=%.8f/%.8f wallet_after=%.8f/%.8f key_quota_before=%.8f key_quota_after=%.8f usage_actual=%.8f usage_units=%.0f dedup=%d",
		job.BatchID, beforeBalance, beforeFrozen, balance, frozen, quotaBefore, quotaUsed, actualCost, units.Float64, 2)
}

func TestBatchImageSettlementQuotaFollowsPerRequestAndPerImageUnits(t *testing.T) {
	ctx := context.Background()
	owner := newBatchImageQuotaOwner(t, 200, 100, 0)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)

	perRequest := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 2, successCount: 2, failCount: 0,
		hold: 2, unitPrice: 0.375, snapshotVersion: 1, mode: service.BillingModePerRequest,
		successImages: []int{2, 1},
	})
	reserveBatchImageQuotaHold(t, billing, perRequest, owner)
	perRequestResult, err := svc.Settle(ctx, perRequest.BatchID)
	require.NoError(t, err)
	require.InDelta(t, 0.75, perRequestResult.ActualCost, 0.00000001)

	perImage := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 2, successCount: 2, failCount: 0,
		hold: 2, unitPrice: 0.375, snapshotVersion: 1, mode: service.BillingModePerImage,
		successImages: []int{2, 1},
	})
	reserveBatchImageQuotaHold(t, billing, perImage, owner)
	perImageResult, err := svc.Settle(ctx, perImage.BatchID)
	require.NoError(t, err)
	require.InDelta(t, 1.125, perImageResult.ActualCost, 0.00000001)

	balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 198.125, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	quotaUsed, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 1.875, quotaUsed, 0.00000001)

	_, perRequestUnits, _, perRequestMode := readBatchImageQuotaUsageLog(t, service.BatchImageCaptureRequestID(perRequest.BatchID))
	require.True(t, perRequestUnits.Valid)
	require.InDelta(t, 2, perRequestUnits.Float64, 0.00000001)
	require.Equal(t, string(service.BillingModePerRequest), perRequestMode.String)

	_, perImageUnits, _, perImageMode := readBatchImageQuotaUsageLog(t, service.BatchImageCaptureRequestID(perImage.BatchID))
	require.True(t, perImageUnits.Valid)
	require.InDelta(t, 3, perImageUnits.Float64, 0.00000001)
	require.Equal(t, string(service.BillingModePerImage), perImageMode.String)
}

func TestBatchImageSettlementQuotaZeroPriceDoesNotCharge(t *testing.T) {
	ctx := context.Background()
	owner := newBatchImageQuotaOwner(t, 100, 100, 1.36)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 1, successCount: 1, failCount: 0,
		hold: 1, unitPrice: 0, snapshotVersion: 1, mode: service.BillingModePerImage,
		successImages: []int{2},
	})
	reserveBatchImageQuotaHold(t, billing, job, owner)

	svc := newBatchImageQuotaSettlement(t, repo, billing, 0)
	result, err := svc.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	require.Zero(t, result.ActualCost)

	balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 100, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	quotaUsed, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 1.36, quotaUsed, 0.00000001)

	actualCost, units, _, _ := readBatchImageQuotaUsageLog(t, service.BatchImageCaptureRequestID(job.BatchID))
	require.Zero(t, actualCost)
	require.True(t, units.Valid)
	require.InDelta(t, 2, units.Float64, 0.00000001)
}

func TestBatchImageSettlementQuotaPreservesLegacyMode(t *testing.T) {
	ctx := context.Background()
	owner := newBatchImageQuotaOwner(t, 100, 100, 1.36)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	// 旧 job：没有 per-image/快照字段，价格仍由 resolver 按成功项单价给出。
	job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 2, successCount: 1, failCount: 1,
		hold: 1, unitPrice: 0.375, snapshotVersion: 0,
		successImages: []int{2}, failedItems: 1,
	})
	reserveBatchImageQuotaHold(t, billing, job, owner)

	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)
	result, err := svc.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	require.InDelta(t, 0.375, result.ActualCost, 0.00000001)

	balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99.625, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	quotaUsed, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 1.735, quotaUsed, 0.00000001)

	actualCost, units, _, _ := readBatchImageQuotaUsageLog(t, service.BatchImageCaptureRequestID(job.BatchID))
	require.InDelta(t, 0.375, actualCost, 0.00000001)
	require.False(t, units.Valid, "legacy billing has no catalog units")
}

func TestBatchImageSettlementQuotaSkipsUnlimitedAndDeletedKeys(t *testing.T) {
	ctx := context.Background()
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)

	t.Run("unlimited_key", func(t *testing.T) {
		owner := newBatchImageQuotaOwner(t, 100, 0, 0)
		job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
			itemCount: 1, successCount: 1, hold: 1, unitPrice: 0.375,
			snapshotVersion: 1, mode: service.BillingModePerImage, successImages: []int{2},
		})
		reserveBatchImageQuotaHold(t, billing, job, owner)
		result, err := svc.Settle(ctx, job.BatchID)
		require.NoError(t, err)
		require.InDelta(t, 0.75, result.ActualCost, 0.00000001)
		balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
		require.InDelta(t, 99.25, balance, 0.00000001)
		require.InDelta(t, 0, frozen, 0.00000001)
		quotaUsed, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
		require.InDelta(t, 0, quotaUsed, 0.00000001, "0 = unlimited, quota_used must stay untouched")
	})

	t.Run("deleted_key", func(t *testing.T) {
		owner := newBatchImageQuotaOwner(t, 100, 100, 1.36)
		job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
			itemCount: 1, successCount: 1, hold: 1, unitPrice: 0.375,
			snapshotVersion: 1, mode: service.BillingModePerImage, successImages: []int{2},
		})
		reserveBatchImageQuotaHold(t, billing, job, owner)
		_, err := integrationDB.ExecContext(ctx, `UPDATE api_keys SET deleted_at = NOW() WHERE id = $1`, owner.apiKeyID)
		require.NoError(t, err)
		result, err := svc.Settle(ctx, job.BatchID)
		require.NoError(t, err)
		require.InDelta(t, 0.75, result.ActualCost, 0.00000001)
		balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
		require.InDelta(t, 99.25, balance, 0.00000001)
		require.InDelta(t, 0, frozen, 0.00000001)
		var quotaUsed float64
		require.NoError(t, integrationDB.QueryRow(`SELECT quota_used FROM api_keys WHERE id = $1`, owner.apiKeyID).Scan(&quotaUsed))
		require.InDelta(t, 1.36, quotaUsed, 0.00000001, "deleted key must be skipped, wallet still settles")
	})
}

func TestBatchImageSettlementQuotaFailureLeavesWalletAndQuotaUntouched(t *testing.T) {
	ctx := context.Background()
	owner := newBatchImageQuotaOwner(t, 100, 100, 1.36)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	// 实际 0.75 超过冻结 0.5：结算在扣款前失败，钱包和 Key 配额都不得变化。
	job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 2, successCount: 1, failCount: 1,
		hold: 0.5, unitPrice: 0.375, snapshotVersion: 1, mode: service.BillingModePerImage,
		successImages: []int{2}, failedItems: 1,
	})
	reserveBatchImageQuotaHold(t, billing, job, owner)
	beforeBalance, beforeFrozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99.5, beforeBalance, 0.00000001)
	require.InDelta(t, 0.5, beforeFrozen, 0.00000001)

	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)
	_, err := svc.Settle(ctx, job.BatchID)
	require.ErrorIs(t, err, service.ErrBatchImageSettlementCostExceedsHold)

	balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99.5, balance, 0.00000001)
	require.InDelta(t, 0.5, frozen, 0.00000001)
	quotaUsed, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 1.36, quotaUsed, 0.00000001)

	failed, err := repo.GetBatchImageJobByBatchID(ctx, job.BatchID)
	require.NoError(t, err)
	require.Equal(t, service.BatchImageJobStatusSettling, failed.Status)
	require.Equal(t, 1, failed.RetryCount)
	require.NotNil(t, failed.LastErrorCode)
	require.Equal(t, "SETTLEMENT_COST_EXCEEDS_HOLD", *failed.LastErrorCode)
	require.Equal(t, 1, countBatchImageQuotaDedup(t, service.BatchImageHoldRequestID(job.BatchID)))

	_, err = billing.ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID:          service.BatchImageReleaseRequestID(job.BatchID),
		APIKeyID:           owner.apiKeyID,
		UserID:             owner.userID,
		BatchID:            job.BatchID,
		HoldAmount:         0.5,
		RequestPayloadHash: batchImageQuotaDeref(job.RequestHash),
	})
	require.NoError(t, err)
	balance, frozen = readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 100, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	quotaUsed, _ = readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 1.36, quotaUsed, 0.00000001)
}

func TestBatchImageSettlementQuotaReplayDoesNotDoubleCharge(t *testing.T) {
	ctx := context.Background()
	owner := newBatchImageQuotaOwner(t, 100, 100, 1.36)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 1, successCount: 1, hold: 1, unitPrice: 0.375,
		snapshotVersion: 1, mode: service.BillingModePerImage, successImages: []int{2},
	})
	reserveBatchImageQuotaHold(t, billing, job, owner)

	// 模拟 capture 已提交、MarkSettled 尚未执行时进程崩溃：重放 Settle 只能补状态，
	// 不得再次扣钱包或再次累加 Key 配额。
	manifestHash := service.BuildBatchImageSettlementManifestHash(job)
	capture, err := billing.CaptureBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID:          service.BatchImageCaptureRequestID(job.BatchID),
		APIKeyID:           owner.apiKeyID,
		UserID:             owner.userID,
		BatchID:            job.BatchID,
		HoldAmount:         1,
		ActualAmount:       0.75,
		RequestPayloadHash: manifestHash,
	})
	require.NoError(t, err)
	require.True(t, capture.Applied)

	balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99.25, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	quotaUsed, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 2.11, quotaUsed, 0.00000001)

	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)
	result, err := svc.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	require.False(t, result.AlreadySettled)
	require.InDelta(t, 0.75, result.ActualCost, 0.00000001)

	balance, frozen = readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99.25, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	quotaUsed, _ = readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 2.11, quotaUsed, 0.00000001)

	replay, err := svc.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	require.True(t, replay.AlreadySettled)
	quotaUsed, _ = readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 2.11, quotaUsed, 0.00000001)

	again, err := billing.CaptureBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID:          service.BatchImageCaptureRequestID(job.BatchID),
		APIKeyID:           owner.apiKeyID,
		UserID:             owner.userID,
		BatchID:            job.BatchID,
		HoldAmount:         1,
		ActualAmount:       0.75,
		RequestPayloadHash: manifestHash,
	})
	require.NoError(t, err)
	require.False(t, again.Applied)

	require.Equal(t, 2, countBatchImageQuotaDedup(t,
		service.BatchImageHoldRequestID(job.BatchID), service.BatchImageCaptureRequestID(job.BatchID)))
}

func TestBatchImageSettlementQuotaDoesNotBackfillHistoricalCapture(t *testing.T) {
	ctx := context.Background()
	owner := newBatchImageQuotaOwner(t, 100, 100, 1.36)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 1, successCount: 1, hold: 1, unitPrice: 0.375,
		snapshotVersion: 1, mode: service.BillingModePerImage, successImages: []int{2},
	})
	reserveBatchImageQuotaHold(t, billing, job, owner)

	// 模拟修复前已提交的 capture：钱包已按 0.75 结算，去重键已占用，但 Key 配额从未累加。
	// 新代码重放该 job 只能补任务状态，不得对历史 capture 补扣配额。
	manifestHash := service.BuildBatchImageSettlementManifestHash(job)
	historical := &service.BatchImageBalanceHoldCommand{
		RequestID:          service.BatchImageCaptureRequestID(job.BatchID),
		APIKeyID:           owner.apiKeyID,
		UserID:             owner.userID,
		BatchID:            job.BatchID,
		HoldAmount:         1,
		ActualAmount:       0.75,
		RequestPayloadHash: manifestHash,
	}
	historical.Normalize()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		UPDATE users
		SET balance = balance + $1,
			frozen_balance = COALESCE(frozen_balance, 0) - $2,
			updated_at = NOW()
		WHERE id = $3
	`, 1.0-0.75, 1.0, owner.userID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
		VALUES ($1, $2, $3)
	`, historical.RequestID, historical.APIKeyID, historical.RequestFingerprint)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)
	result, err := svc.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	require.InDelta(t, 0.75, result.ActualCost, 0.00000001)

	balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99.25, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	quotaUsed, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 1.36, quotaUsed, 0.00000001, "historical capture must not be backfilled")
}

func TestBatchImageSettlementQuotaExhaustionMarksKey(t *testing.T) {
	ctx := context.Background()
	// 与普通 APIKeyQuotaCost 相同：累加跨过 quota 时把 active 置为 quota_exhausted。
	owner := newBatchImageQuotaOwner(t, 100, 2, 1.36)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 1, successCount: 1, hold: 1, unitPrice: 0.375,
		snapshotVersion: 1, mode: service.BillingModePerImage, successImages: []int{2},
	})
	reserveBatchImageQuotaHold(t, billing, job, owner)

	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)
	_, err := svc.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	quotaUsed, status := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 2.11, quotaUsed, 0.00000001)
	require.Equal(t, service.StatusAPIKeyQuotaExhausted, status)
}

func TestBatchImageSettlementQuotaConcurrentSettleChargesOnce(t *testing.T) {
	ctx := context.Background()
	owner := newBatchImageQuotaOwner(t, 100, 100, 1.36)
	repo := NewBatchImageRepository(integrationDB)
	billing := NewUsageBillingRepository(testEntClient(t), integrationDB)
	job := newBatchImageQuotaJob(t, repo, owner, batchImageQuotaJobOptions{
		itemCount: 1, successCount: 1, hold: 1, unitPrice: 0.375,
		snapshotVersion: 1, mode: service.BillingModePerImage, successImages: []int{2},
	})
	reserveBatchImageQuotaHold(t, billing, job, owner)

	svc := newBatchImageQuotaSettlement(t, repo, billing, 0.375)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = svc.Settle(ctx, job.BatchID)
		}(i)
	}
	close(start)
	wg.Wait()

	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		}
	}
	require.GreaterOrEqual(t, success, 1, "at least one Settle must complete: %v", errs)

	balance, frozen := readBatchImageQuotaWallet(t, owner.userID)
	require.InDelta(t, 99.25, balance, 0.00000001)
	require.InDelta(t, 0, frozen, 0.00000001)
	quotaUsed, _ := readBatchImageQuotaKey(t, owner.apiKeyID)
	require.InDelta(t, 2.11, quotaUsed, 0.00000001)

	require.Equal(t, 2, countBatchImageQuotaDedup(t,
		service.BatchImageHoldRequestID(job.BatchID), service.BatchImageCaptureRequestID(job.BatchID)))
	var usageRows int
	require.NoError(t, integrationDB.QueryRow(
		`SELECT COUNT(*) FROM usage_logs WHERE request_id = $1`,
		service.BatchImageCaptureRequestID(job.BatchID),
	).Scan(&usageRows))
	require.Equal(t, 1, usageRows)
}
