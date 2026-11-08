package service

import (
	"context"
	"encoding/json"
	"fmt"
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

// SetCNYOfficialPricingSettings 写入配置。
//
// 校验规则（写坏配置会让整轮同步失败，所以在这里拦）：
//   - manual_overrides 的键必须是非空的 vendor/model 形式
//   - 价格必须是非负有限数
//   - source_url 留空时回落到默认数据源
func (s *SettingService) SetCNYOfficialPricingSettings(ctx context.Context, settings *CNYOfficialPricingSettings) (*CNYOfficialPricingSettings, error) {
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
		if !strings.Contains(key, "/") {
			return nil, fmt.Errorf("manual_overrides 的模型键 %q 必须形如 vendor/model", rawKey)
		}
		if _, dup := normalized.ManualOverrides[key]; dup {
			return nil, fmt.Errorf("manual_overrides 模型键 %q 重复（大小写或空格不同）", rawKey)
		}
		price := settings.ManualOverrides[rawKey]
		for name, value := range map[string]float64{
			"input":      price.Input,
			"output":     price.Output,
			"cache_read": price.CacheRead,
		} {
			if value < 0 {
				return nil, fmt.Errorf("manual_overrides[%s].%s 不能为负", key, name)
			}
		}
		normalized.ManualOverrides[key] = price
	}

	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", SettingKeyPricingCNYOfficial, err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyPricingCNYOfficial, string(data)); err != nil {
		return nil, fmt.Errorf("write %s: %w", SettingKeyPricingCNYOfficial, err)
	}
	return normalized, nil
}
