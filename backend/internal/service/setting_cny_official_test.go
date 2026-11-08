package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// CNY 口径配置的读写回归。
//
// 背景：前端「计价口径配置」卡片一度调用了不存在的端点（GET/PUT
// /admin/model-pricing-sync/cny-official 未注册），卡片只能显示「后端未注册」。
// 这组测试钉住服务层读写语义，handler 侧由此端点转发。

func newCNYSettingsService() (*SettingService, *inMemorySettingRepo) {
	repo := &inMemorySettingRepo{values: map[string]string{}}
	return NewSettingService(repo, nil), repo
}

// TestSetCNYOfficialPricingSettings_RoundTrip 写入后读回必须一致。
func TestSetCNYOfficialPricingSettings_RoundTrip(t *testing.T) {
	svc, _ := newCNYSettingsService()
	ctx := context.Background()

	_, err := svc.SetCNYOfficialPricingSettings(ctx, &CNYOfficialPricingSettings{
		Enabled:   true,
		SourceURL: "https://example.com/all.json",
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"deepseek/deepseek-flash": {Input: 1, Output: 4, CacheRead: 0.02},
		},
	})
	require.NoError(t, err)

	got, err := svc.GetCNYOfficialPricingSettings(ctx)
	require.NoError(t, err)
	require.True(t, got.Enabled)
	require.Equal(t, "https://example.com/all.json", got.SourceURL)
	require.Len(t, got.ManualOverrides, 1)
	price := got.ManualOverrides["deepseek/deepseek-flash"]
	require.InDelta(t, 1, price.Input, 1e-9)
	require.InDelta(t, 4, price.Output, 1e-9)
	require.InDelta(t, 0.02, price.CacheRead, 1e-12)
}

// TestGetCNYOfficialPricingSettings_DefaultsWhenMissing 键不存在时给出可用默认值，
// 而不是 nil（前端要能直接渲染表单）。
func TestGetCNYOfficialPricingSettings_DefaultsWhenMissing(t *testing.T) {
	svc, _ := newCNYSettingsService()
	got, err := svc.GetCNYOfficialPricingSettings(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)
	require.False(t, got.Enabled, "未配置时默认关闭")
	require.Equal(t, cnyOfficialDefaultSourceURL, got.SourceURL, "空 source_url 应回落默认数据源")
	require.Empty(t, got.ManualOverrides)
}

// TestSetCNYOfficialPricingSettings_RejectsBadKeys 模型键必须形如 vendor/model。
func TestSetCNYOfficialPricingSettings_RejectsBadKeys(t *testing.T) {
	svc, _ := newCNYSettingsService()
	ctx := context.Background()

	for _, bad := range []string{"deepseek-flash", "", "   "} {
		_, err := svc.SetCNYOfficialPricingSettings(ctx, &CNYOfficialPricingSettings{
			Enabled:         true,
			ManualOverrides: map[string]CNYOfficialManualPrice{bad: {Input: 1}},
		})
		require.Error(t, err, "键 %q 应当被拒绝", bad)
	}
}

// TestSetCNYOfficialPricingSettings_RejectsNegativePrice 价格不能为负。
func TestSetCNYOfficialPricingSettings_RejectsNegativePrice(t *testing.T) {
	svc, _ := newCNYSettingsService()
	_, err := svc.SetCNYOfficialPricingSettings(context.Background(), &CNYOfficialPricingSettings{
		Enabled:         true,
		ManualOverrides: map[string]CNYOfficialManualPrice{"deepseek/deepseek-flash": {Input: -1}},
	})
	require.Error(t, err)
}

// TestSetCNYOfficialPricingSettings_NormalizesKeys 键做小写+去空格归一。
func TestSetCNYOfficialPricingSettings_NormalizesKeys(t *testing.T) {
	svc, _ := newCNYSettingsService()
	ctx := context.Background()

	saved, err := svc.SetCNYOfficialPricingSettings(ctx, &CNYOfficialPricingSettings{
		Enabled:         true,
		ManualOverrides: map[string]CNYOfficialManualPrice{"  DeepSeek/DeepSeek-Flash  ": {Input: 1}},
	})
	require.NoError(t, err)
	_, ok := saved.ManualOverrides["deepseek/deepseek-flash"]
	require.True(t, ok, "键应被归一成小写去空格形式")
}

// TestSetCNYOfficialPricingSettings_RejectsDuplicateAfterNormalize
// 归一后冲突的键必须拒绝，否则静默丢一条。
func TestSetCNYOfficialPricingSettings_RejectsDuplicateAfterNormalize(t *testing.T) {
	svc, _ := newCNYSettingsService()
	_, err := svc.SetCNYOfficialPricingSettings(context.Background(), &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"deepseek/deepseek-flash":  {Input: 1},
			"DEEPSEEK/deepseek-flash ": {Input: 2},
		},
	})
	require.Error(t, err, "归一后重复的键必须报错，不能静默丢一条")
}

// TestSetCNYOfficialPricingSettings_WrittenValueIsReadableBySync
// 写入的 JSON 必须能被同步侧的解析函数直接读回 —— 这是端点与同步任务之间的契约。
func TestSetCNYOfficialPricingSettings_WrittenValueIsReadableBySync(t *testing.T) {
	svc, repo := newCNYSettingsService()
	ctx := context.Background()

	_, err := svc.SetCNYOfficialPricingSettings(ctx, &CNYOfficialPricingSettings{
		Enabled:         true,
		ManualOverrides: map[string]CNYOfficialManualPrice{"deepseek/deepseek-flash": {Input: 1, Output: 4}},
	})
	require.NoError(t, err)

	raw := repo.values[SettingKeyPricingCNYOfficial]
	require.NotEmpty(t, raw)

	// 同步侧正是这样读的
	parsed, err := parseCNYOfficialPricingSettings(raw)
	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.True(t, parsed.Enabled)
	require.Equal(t, cnyOfficialDefaultSourceURL, parsed.SourceURL)
	require.Len(t, parsed.ManualOverrides, 1)

	// 且能被 dataset 判定为 manual 命中
	ds := &cnyOfficialDataset{ManualOverrides: map[string]struct{}{"deepseek/deepseek-flash": {}}}
	require.True(t, ds.manualOverrideHit("deepseek/deepseek-flash", "deepseek"))
}

// TestCNYOfficialSettingsJSONShape 钉住落库 JSON 的字段名，前端按此读写。
func TestCNYOfficialSettingsJSONShape(t *testing.T) {
	svc, repo := newCNYSettingsService()
	_, err := svc.SetCNYOfficialPricingSettings(context.Background(), &CNYOfficialPricingSettings{
		Enabled:         true,
		ManualOverrides: map[string]CNYOfficialManualPrice{"deepseek/deepseek-flash": {Input: 1, Output: 4, CacheRead: 0.02}},
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyPricingCNYOfficial]), &decoded))
	require.Contains(t, decoded, "enabled")
	require.Contains(t, decoded, "source_url")
	require.Contains(t, decoded, "manual_overrides")

	overrides, ok := decoded["manual_overrides"].(map[string]any)
	require.True(t, ok)
	entry, ok := overrides["deepseek/deepseek-flash"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, entry, "input")
	require.Contains(t, entry, "output")
	require.Contains(t, entry, "cache_read")
}

// TestSetCNYOfficialPricingSettings_LongContextValidation 校验长上下文三字段：
// 写坏配置会让计费侧读到「有阈值但倍率无效」的组合，必须在写入前拦住。
func TestSetCNYOfficialPricingSettings_LongContextValidation(t *testing.T) {
	cases := []struct {
		name  string
		price CNYOfficialManualPrice
		want  string
	}{
		{
			name:  "负阈值被拒",
			price: CNYOfficialManualPrice{Input: 1, Output: 4, LongContextThreshold: -1},
			want:  "long_context_threshold 不能为负",
		},
		{
			name:  "倍率小于1被拒",
			price: CNYOfficialManualPrice{Input: 1, Output: 4, LongContextThreshold: 32000, LongContextInputMultiplier: 0.5},
			want:  "必须 ≥1",
		},
		{
			name:  "只有倍率没有阈值被拒",
			price: CNYOfficialManualPrice{Input: 1, Output: 4, LongContextOutputMultiplier: 2},
			want:  "倍率不会生效",
		},
		{
			name:  "负倍率被拒",
			price: CNYOfficialManualPrice{Input: 1, Output: 4, LongContextThreshold: 32000, LongContextInputMultiplier: -2},
			want:  "不能为负",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newCNYSettingsService()
			_, err := svc.SetCNYOfficialPricingSettings(context.Background(), &CNYOfficialPricingSettings{
				Enabled:         true,
				ManualOverrides: map[string]CNYOfficialManualPrice{"deepseek/deepseek-flash": tc.price},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}

	// 阈值 0 + 倍率留空 = 不启用阶梯，合法。
	svc, _ := newCNYSettingsService()
	got, err := svc.SetCNYOfficialPricingSettings(context.Background(), &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"deepseek/deepseek-flash": {Input: 1, Output: 4},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 0, got.ManualOverrides["deepseek/deepseek-flash"].LongContextThreshold)

	// 完整配置留得下、读得回。
	svc2, _ := newCNYSettingsService()
	got2, err := svc2.SetCNYOfficialPricingSettings(context.Background(), &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"deepseek/deepseek-flash": {
				Input: 1, Output: 4, CacheRead: 0.02,
				LongContextThreshold:        32000,
				LongContextInputMultiplier:  1.5,
				LongContextOutputMultiplier: 2,
			},
		},
	})
	require.NoError(t, err)
	saved := got2.ManualOverrides["deepseek/deepseek-flash"]
	require.Equal(t, 32000, saved.LongContextThreshold)
	require.InDelta(t, 1.5, saved.LongContextInputMultiplier, 1e-9)
	require.InDelta(t, 2, saved.LongContextOutputMultiplier, 1e-9)
}
