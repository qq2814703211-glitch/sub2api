package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// CNY 官方人民币价的配置读写。
//
// 这个配置不走 UpdateSettingsRequest 的白名单（那里未列出的 JSON 键会被
// encoding/json 静默丢弃，提交后返回 200 却什么都没写），而是走独立端点 +
// 这里的专用方法，语义与 setting_features.go 里其他 Settings 一致。

// GetCNYOfficialPricingSettings 读取当前配置。
// 键不存在时返回一个「启用状态与默认一致、无手动覆盖」的对象，便于前端渲染。
func (s *SettingService) GetCNYOfficialPricingSettings(ctx context.Context) (*CNYOfficialPricingSettings, error) {
	values, err := s.GetRawSettings(ctx, []string{SettingKeyPricingCNYOfficial})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SettingKeyPricingCNYOfficial, err)
	}
	settings, err := parseCNYOfficialPricingSettings(values[SettingKeyPricingCNYOfficial])
	if err != nil {
		return nil, err
	}
	if settings == nil {
		settings = &CNYOfficialPricingSettings{}
	}
	if strings.TrimSpace(settings.SourceURL) == "" {
		settings.SourceURL = cnyOfficialDefaultSourceURL
	}
	return settings, nil
}

func normalizePriceSyncSettings(settings *CNYOfficialPricingSettings) (*CNYOfficialPricingSettings, error) {
	if settings == nil {
		return nil, fmt.Errorf("settings cannot be nil")
	}

	normalized := &CNYOfficialPricingSettings{
		Enabled:         settings.Enabled,
		SourceURL:       strings.TrimSpace(settings.SourceURL),
		ManualOverrides: make(map[string]CNYOfficialManualPrice, len(settings.ManualOverrides)),
	}
	if normalized.SourceURL == "" {
		normalized.SourceURL = cnyOfficialDefaultSourceURL
	}

	keys := make([]string, 0, len(settings.ManualOverrides))
	for key := range settings.ManualOverrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, rawKey := range keys {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		if key == "" {
			return nil, fmt.Errorf("manual_overrides 含空模型键")
		}
		// 键原样保留，不剥前缀：覆盖层的键就是模型请求名，而真实模型名本身
		// 可能带斜杠（生产里存在 `siliconflow/deepseek-v3.2` 这类真条目）。
		// 剥掉前缀会把真模型写成裸名，顺带覆盖同名的另一条。
		if _, dup := normalized.ManualOverrides[key]; dup {
			return nil, fmt.Errorf("manual_overrides 模型键 %q 重复（大小写或空格不同）", rawKey)
		}
		price := settings.ManualOverrides[rawKey]
		for name, ptr := range map[string]*float64{"cache_write_30m": price.CacheWrite30m, "cache_write_12h": price.CacheWrite12h, "per_request_price": price.PerRequestPrice, "per_second_price": price.PerSecondPrice} {
			if ptr != nil && !priceSyncFinite(*ptr) {
				return nil, fmt.Errorf("manual_overrides[%s].%s 必须为非负有限数", key, name)
			}
		}
		negative := map[string]float64{
			"input":       price.Input,
			"output":      price.Output,
			"cache_read":  price.CacheRead,
			"cache_write": price.CacheWrite,
		}
		// 可选计费项同样不能为负：负价会让计费侧算出负费用（等于给用户退款）。
		for _, extra := range cnyOfficialManualExtraFields {
			negative[extra.Key] = price.extraValue(extra.Key)
		}
		names := make([]string, 0, len(negative))
		for name := range negative {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !priceSyncFinite(negative[name]) {
				return nil, fmt.Errorf("manual_overrides[%s].%s 不能为负", key, name)
			}
		}
		// 长上下文阶梯：阈值必须是 ≥0 的整数（0 = 不启用），倍率留空按 0 存、
		// 填了必须 ≥1。写坏这三个字段会让计费侧读到「有阈值但倍率无效」的组合。
		if price.LongContextThreshold < 0 {
			return nil, fmt.Errorf("manual_overrides[%s].long_context_threshold 不能为负", key)
		}
		for name, value := range map[string]float64{
			"long_context_input_multiplier":  price.LongContextInputMultiplier,
			"long_context_output_multiplier": price.LongContextOutputMultiplier,
		} {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("manual_overrides[%s].%s 不能为负", key, name)
			}
			if value > 0 && value < 1 {
				return nil, fmt.Errorf("manual_overrides[%s].%s 必须 ≥1（留空表示不涨价）", key, name)
			}
		}
		// 只在配了倍率却没有阈值时拦：倍率没有阈值不生效，属于静默失效的配置。
		if price.LongContextThreshold == 0 &&
			(price.LongContextInputMultiplier > 0 || price.LongContextOutputMultiplier > 0) {
			return nil, fmt.Errorf(
				"manual_overrides[%s] 配置了长上下文倍率但 long_context_threshold 为空，倍率不会生效", key)
		}
		normalized.ManualOverrides[key] = price
	}

	return normalized, nil
}
