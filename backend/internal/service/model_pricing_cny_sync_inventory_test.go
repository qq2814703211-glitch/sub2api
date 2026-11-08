//go:build unit

package service

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCNYOfficial_RealSnapshotUnrepresentableInventory 打印真实数据源里
// 站内覆盖层表达不了、或需要单独跟进的部分，供发布报告引用。
// 断言只保证清单非空且结构稳定；具体条目随上游更新变动。
func TestCNYOfficial_RealSnapshotUnrepresentableInventory(t *testing.T) {
	registry := mustParseCNYRegistry(t, readTestdata(t, "testdata/llm-metadata-all.json"))
	dataset := buildCNYOfficialDataset(registry, &CNYOfficialPricingSettings{Enabled: true}, "snapshot")
	require.NotEmpty(t, dataset.Pricing)

	multiTier := make([]string, 0)
	for model, priced := range dataset.Pricing {
		if priced.LongContext != nil && priced.LongContext.TierCount > 1 {
			multiTier = append(multiTier, model)
		}
	}
	sort.Strings(multiTier)
	t.Logf("多档阶梯（只表达第 1 档）%d 个: %v", len(multiTier), multiTier)

	withSchedule := make([]string, 0)
	unrepresentableSchedule := make([]string, 0)
	for model, priced := range dataset.Pricing {
		if priced.TimePricing == nil {
			continue
		}
		withSchedule = append(withSchedule, model)
		if priced.TimePricing.ChannelTimePricing == nil {
			unrepresentableSchedule = append(unrepresentableSchedule, model)
		}
	}
	sort.Strings(withSchedule)
	sort.Strings(unrepresentableSchedule)
	t.Logf("有分时谷峰 %d 个: %v", len(withSchedule), withSchedule)
	t.Logf("分时无法落 ChannelTimePricing %d 个: %v", len(unrepresentableSchedule), unrepresentableSchedule)

	t.Logf("倍率/精度/时区冲突 %d 条:", len(dataset.Report.Conflicts))
	for _, conflict := range dataset.Report.Conflicts {
		t.Logf("  - %s", conflict)
	}

	t.Logf("国模无人民币价、已跳过（不回退美元）%d 个: %v",
		len(dataset.Report.SkippedNoCNY), dataset.Report.SkippedNoCNY)
}
