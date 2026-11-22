package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

func (s *SettingService) SetCNYOfficialPricingSettings(ctx context.Context, settings *CNYOfficialPricingSettings) (*CNYOfficialPricingSettings, error) {
	normalized, err := normalizePriceSyncSettings(settings)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyPricingCNYOfficial, string(data)); err != nil {
		return nil, err
	}
	return normalized, nil
}

// Frozen V1 behavioral harness. No legacy writer is linked into the server.
func syncLegacyForTest(s *ModelPricingSyncService, ctx context.Context) (int, error) {
	if !s.SyncEnabled() {
		return 0, fmt.Errorf("model pricing sync requires pricing.override_file")
	}
	cnySettings, err := loadCNYOfficialPricingSettings(ctx, s.settings)
	if err != nil {
		// 配置读不出来时既不能当关闭（会把国模改回美元），也不能猜：整轮放弃。
		s.recordRun(err, 0)
		s.recordCNYRun(err, nil, 0)
		return 0, err
	}
	cnyEnabled := cnySettings != nil && cnySettings.Enabled

	var dataset *cnyOfficialDataset
	if cnyEnabled {
		dataset, err = s.loadCNYOfficialDataset(ctx, cnySettings)
		if err != nil {
			s.recordRun(err, 0)
			s.recordCNYRun(err, nil, 0)
			return 0, err
		}
		logCNYOfficialReport(dataset.Report)
	}

	registry, err := s.fetchRegistry(ctx)
	if err != nil {
		s.recordRun(err, 0)
		s.recordCNYRun(nil, nil, 0)
		return 0, err
	}
	reference := buildModelPricingSyncReference(registry)
	if len(reference) == 0 && (dataset == nil || len(dataset.Pricing) == 0) {
		err := fmt.Errorf("models.dev registry contains no usable pricing")
		s.recordRun(err, 0)
		s.recordCNYRun(nil, nil, 0)
		return 0, err
	}

	existing, err := s.loadOverrideEntries()
	if err != nil {
		s.recordRun(err, 0)
		s.recordCNYRun(nil, nil, 0)
		return 0, fmt.Errorf("read override file: %w", err)
	}
	syncedFields, _, cnyWrittenHist, err := s.loadSyncedFieldsWithCNY(ctx)
	if err != nil {
		// 变更史读不到时按「全部都是手动定价」处理：宁可不写，也不覆盖手配价。
		s.recordRun(err, 0)
		s.recordCNYRun(nil, nil, 0)
		return 0, fmt.Errorf("read pricing sync history: %w", err)
	}

	changes := mergeModelPricingTierChanges(existing, reference, dataset, syncedFields)
	report := cnyOfficialReportOf(dataset)
	if report != nil {
		report.Applied = countSyncModelsBySource(changes, ModelPricingSyncSourceCNYOfficial)
		report.Skipped = len(dataset.Pricing) - report.Applied
		if report.Skipped < 0 {
			report.Skipped = 0
		}
	}
	if len(changes) == 0 {
		s.recordRun(nil, 0)
		s.recordCNYRun(nil, report, 0)
		return 0, nil
	}

	merged := applyModelPricingSyncChanges(existing, changes, cnyOfficialModelSet(dataset), cnyWrittenModelSet(changes, cnyWrittenHist))
	if err := s.writeOverrideFile(merged); err != nil {
		s.recordRun(err, 0)
		s.recordCNYRun(err, nil, 0)
		return 0, err
	}
	// 写完立刻走 PricingService 已有的文件指纹热重载路径，不需要重启进程。
	if s.pricing != nil {
		s.pricing.reloadIfCustomFilesChanged()
	}
	s.recordHistory(ctx, changes)
	appliedCNY := countSyncModelsBySource(changes, ModelPricingSyncSourceCNYOfficial)
	s.recordRun(nil, len(changes))
	if report != nil {
		report.Applied = appliedCNY
		report.Skipped = len(dataset.Pricing) - appliedCNY
		if report.Skipped < 0 {
			report.Skipped = 0
		}
	}
	s.recordCNYRun(nil, report, appliedCNY)
	slog.Info("model pricing sync applied",
		"changes", len(changes),
		"models", countSyncModels(changes),
		"cny_models", appliedCNY)
	return len(changes), nil
}
