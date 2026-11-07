package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// V3 是独立产品，不是 V1/V2 的一个 mode（见 ADR 0008）。
//
// 本文件定义 V3 自己的配置模型与仓库契约。V3 的开关、分组名单与探针身份全部
// 来自 `channel_monitor_v3_config`（单行 id=1），不再读取 `channel_monitor_enabled`
// / `channel_monitor_mode`（全局开关与模式枚举），也不再看分组的 model_allowlist /
// model_pricing / platforms 前置 —— 用户明确要求去掉这些门槛。

var (
	// ErrChannelMonitorV3InvalidConfig 配置校验失败（handler 映射 400）。
	ErrChannelMonitorV3InvalidConfig = errors.New("invalid channel monitor v3 config")
	// ErrChannelMonitorV3ConfigConflict 乐观锁版本冲突（handler 映射 409）。
	ErrChannelMonitorV3ConfigConflict = errors.New("channel monitor v3 config was modified")
)

const (
	// ChannelMonitorV3DefaultCacheAverageWindowDays 探针桶缓存率的默认回看窗口。
	// ADR 0009：探针桶不产出缓存率，改用分组最近 N 天真实流量的缓存率均值。
	ChannelMonitorV3DefaultCacheAverageWindowDays = 30
	// ChannelMonitorV3MaxCacheAverageWindowDays 窗口上限（与 V2 的 90 天保留期对齐）。
	ChannelMonitorV3MaxCacheAverageWindowDays = 90
	// ChannelMonitorV3DefaultIntervalSeconds 未配置 interval 时的默认探针间隔。
	ChannelMonitorV3DefaultIntervalSeconds = 300
	// ChannelMonitorV3DisabledIntervalFallback V3 关掉后 runner 的轮询间隔。
	ChannelMonitorV3DisabledIntervalFallback = 60 * time.Second
)

// ChannelMonitorV3Group 是 V3 看板上的一个分组条目。
//
// Models 为空表示「该分组不按模型拆分」，只有 ProbeEnabled 的分组才会被主动探针填充。
type ChannelMonitorV3Group struct {
	GroupID      int64    `json:"group_id"`
	Enabled      bool     `json:"enabled"`
	Models       []string `json:"models"`
	ProbeEnabled bool     `json:"probe_enabled"`
}

// ChannelMonitorV3Probe 是 V3 自己的一条监控专用探针身份（不再复用 channel_monitors）。
//
// APIKeyEncrypted 存 AES-256-GCM 密文；service 对外返回前会清空，只保留 ApiKeySet 标记。
type ChannelMonitorV3Probe struct {
	Endpoint        string   `json:"endpoint"`
	APIKeyEncrypted string   `json:"api_key_encrypted,omitempty"`
	Provider        string   `json:"provider"`
	PrimaryModel    string   `json:"primary_model"`
	ExtraModels     []string `json:"extra_models"`
	IntervalSeconds int      `json:"interval_seconds"`
}

// ChannelMonitorV3Config 对应 channel_monitor_v3_config 单行表。
type ChannelMonitorV3Config struct {
	Version                int                     `json:"version"`
	Enabled                bool                    `json:"enabled"`
	Groups                 []ChannelMonitorV3Group `json:"groups"`
	Probe                  ChannelMonitorV3Probe   `json:"probe"`
	CacheAverageWindowDays int                     `json:"cache_average_window_days"`
	UpdatedAt              time.Time               `json:"updated_at"`
	UpdatedBy              *int64                  `json:"updated_by,omitempty"`

	// APIKeySet 是响应侧派生字段：探针 key 是否已配置。不回传密文本身。
	APIKeySet bool `json:"api_key_set"`
}

// EnabledGroupIDs 返回所有启用分组的 ID（升序），V3 只监控这些分组。
func (c ChannelMonitorV3Config) EnabledGroupIDs() []int64 {
	ids := make([]int64, 0, len(c.Groups))
	for _, g := range c.Groups {
		if g.Enabled && g.GroupID > 0 {
			ids = append(ids, g.GroupID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return normalizeInt64Set(ids)
}

// ProbeGroups 返回需要主动探针填充的分组（已启用 且 probe_enabled）。
func (c ChannelMonitorV3Config) ProbeGroups() []ChannelMonitorV3Group {
	out := make([]ChannelMonitorV3Group, 0, len(c.Groups))
	for _, g := range c.Groups {
		if g.Enabled && g.ProbeEnabled && g.GroupID > 0 {
			out = append(out, g)
		}
	}
	return out
}

// ProbeModels 返回探针要打的模型列表：primary 在前，extra 去重追加。
func (p ChannelMonitorV3Probe) ProbeModels() []string {
	models := make([]string, 0, 1+len(p.ExtraModels))
	seen := make(map[string]struct{}, 1+len(p.ExtraModels))
	primary := strings.TrimSpace(p.PrimaryModel)
	if primary != "" {
		models = append(models, primary)
		seen[primary] = struct{}{}
	}
	for _, raw := range p.ExtraModels {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	return models
}

// ChannelMonitorV3ProbeSample 是一次 V3 探针结果（写入 channel_monitor_v3_probe_samples）。
//
// 只保留矩阵兜底需要的字段：分组、成功与否、首字延迟、时间。缓存率不在这里 ——
// 探针请求本身几乎不用缓存（ADR 0009），缓存率由分组历史真实流量均值填充。
type ChannelMonitorV3ProbeSample struct {
	GroupID   int64
	Success   bool
	TTFTMs    *int
	CheckedAt time.Time
}

// ChannelMonitorV3GroupInfo 是分组表里 V3 看板需要的展示信息。
type ChannelMonitorV3GroupInfo struct {
	Name     string
	Platform string
}

// ChannelMonitorV3Repository 是 V3 的持久化契约。
type ChannelMonitorV3Repository interface {
	// GetConfig 读取单行配置；行缺失时返回默认值（enabled=false）。
	GetConfig(ctx context.Context) (*ChannelMonitorV3Config, error)
	// UpdateConfig 以 expectedVersion 做乐观锁更新。
	UpdateConfig(ctx context.Context, cfg ChannelMonitorV3Config, expectedVersion int) (*ChannelMonitorV3Config, error)

	// InsertProbeSamples 批量写入探针样本。
	InsertProbeSamples(ctx context.Context, samples []ChannelMonitorV3ProbeSample) error
	// ListProbeSamples 取时间窗内全部探针样本（按 checked_at 升序）。
	ListProbeSamples(ctx context.Context, start, end time.Time) ([]ChannelMonitorV3ProbeSample, error)

	// LoadMonitorableGroups 读取分组名与平台，供设置页勾选与看板 seed 使用。
	// 不涉及 model_allowlist / model_pricing：V3 的前置只有「被勾选」。
	LoadMonitorableGroups(ctx context.Context, groupIDs []int64) (map[int64]ChannelMonitorV3GroupInfo, error)
	// ListMonitorableGroups 列出全部可监控分组（设置页候选）。
	ListMonitorableGroups(ctx context.Context) ([]ChannelMonitorV3MonitorableGroup, error)

	// GroupCacheRates 取分组最近 windowDays 天真实流量的缓存率均值。
	// 返回 map[group_id] = 缓存率（0..1）；没有真实流量可算均值的分组不出现在 map 里。
	GroupCacheRates(ctx context.Context, groupIDs []int64, windowDays int) (map[int64]float64, error)

	// GroupHasOrganicTrafficSince 报告分组自 since 起是否有真实用户流量（探针不计）。
	// 有真实流量的分组不需要探针填桶，探针省下上游配额。
	GroupHasOrganicTrafficSince(ctx context.Context, groupID int64, since time.Time) (bool, error)
}

// ChannelMonitorV3MonitorableGroup 是设置页候选分组（含是否已在 V3 名单里的原始信息不上这里）。
type ChannelMonitorV3MonitorableGroup struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
}

// ChannelMonitorV3Service 是 V3 配置服务（管理端读写 + 用户端可用性）。
type ChannelMonitorV3Service struct {
	repo      ChannelMonitorV3Repository
	encryptor SecretEncryptor
	now       func() time.Time
}

// NewChannelMonitorV3Service 创建 V3 配置服务。
func NewChannelMonitorV3Service(repo ChannelMonitorV3Repository, encryptor SecretEncryptor) *ChannelMonitorV3Service {
	return &ChannelMonitorV3Service{repo: repo, encryptor: encryptor, now: func() time.Time { return time.Now().UTC() }}
}

// GetConfig 读取 V3 配置（密文已清空，只留 api_key_set 标记）。
func (s *ChannelMonitorV3Service) GetConfig(ctx context.Context) (*ChannelMonitorV3Config, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	return redactChannelMonitorV3Config(cfg), nil
}

// PublicStatus 是用户端 V3 页面的前置探测：只回 enabled，不泄露配置细节。
func (s *ChannelMonitorV3Service) PublicStatus(ctx context.Context) (map[string]bool, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"enabled": cfg != nil && cfg.Enabled}, nil
}

// UpdateConfig 校验并保存 V3 配置。
//
// plainAPIKey 非空时加密覆盖探针 key；为空表示「保留原 key」。这样设置页不必回填明文。
func (s *ChannelMonitorV3Service) UpdateConfig(
	ctx context.Context,
	cfg ChannelMonitorV3Config,
	plainAPIKey string,
	expectedVersion int,
	actorID int64,
) (*ChannelMonitorV3Config, error) {
	if err := normalizeChannelMonitorV3Config(&cfg); err != nil {
		return nil, err
	}
	if err := validateChannelMonitorV3Probe(cfg.Probe.Endpoint, cfg.Probe.Provider, cfg.Probe.PrimaryModel, cfg.Probe.IntervalSeconds); err != nil {
		return nil, err
	}
	existing, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(plainAPIKey)
	switch {
	case key != "":
		if s.encryptor == nil {
			return nil, fmt.Errorf("%w: secret encryptor unavailable", ErrChannelMonitorV3InvalidConfig)
		}
		encrypted, encErr := s.encryptor.Encrypt(key)
		if encErr != nil {
			return nil, fmt.Errorf("encrypt channel monitor v3 api key: %w", encErr)
		}
		cfg.Probe.APIKeyEncrypted = encrypted
	case existing != nil:
		// 未填新 key：沿用已存的密文，避免把已配置的探针身份清空。
		cfg.Probe.APIKeyEncrypted = existing.Probe.APIKeyEncrypted
	}
	if cfg.Enabled {
		// 开启 V3 必须有一条可用的探针身份或至少一个分组；否则开启后什么也不会跑，
		// 页面却是「已启用」，比直接报错更难排查。
		if len(cfg.EnabledGroupIDs()) == 0 {
			return nil, fmt.Errorf("%w: at least one enabled group is required", ErrChannelMonitorV3InvalidConfig)
		}
	}
	cfg.UpdatedBy = &actorID
	updated, err := s.repo.UpdateConfig(ctx, cfg, expectedVersion)
	if err != nil {
		return nil, err
	}
	return redactChannelMonitorV3Config(updated), nil
}

// ListMonitorableGroups 返回设置页可勾选的分组（不需要白名单/价卡）。
func (s *ChannelMonitorV3Service) ListMonitorableGroups(ctx context.Context) ([]ChannelMonitorV3MonitorableGroup, error) {
	return s.repo.ListMonitorableGroups(ctx)
}

// redactChannelMonitorV3Config 清空密文并派生 api_key_set。
func redactChannelMonitorV3Config(cfg *ChannelMonitorV3Config) *ChannelMonitorV3Config {
	if cfg == nil {
		return nil
	}
	cfg.APIKeySet = strings.TrimSpace(cfg.Probe.APIKeyEncrypted) != ""
	cfg.Probe.APIKeyEncrypted = ""
	if cfg.Groups == nil {
		cfg.Groups = []ChannelMonitorV3Group{}
	}
	if cfg.Probe.ExtraModels == nil {
		cfg.Probe.ExtraModels = []string{}
	}
	return cfg
}

// normalizeChannelMonitorV3Config 归一化并校验分组名单 / 窗口 / 间隔。
func normalizeChannelMonitorV3Config(cfg *ChannelMonitorV3Config) error {
	if cfg.CacheAverageWindowDays <= 0 {
		cfg.CacheAverageWindowDays = ChannelMonitorV3DefaultCacheAverageWindowDays
	}
	if cfg.CacheAverageWindowDays > ChannelMonitorV3MaxCacheAverageWindowDays {
		return fmt.Errorf("%w: cache_average_window_days must be between 1 and %d",
			ErrChannelMonitorV3InvalidConfig, ChannelMonitorV3MaxCacheAverageWindowDays)
	}
	if cfg.Probe.IntervalSeconds <= 0 {
		cfg.Probe.IntervalSeconds = ChannelMonitorV3DefaultIntervalSeconds
	}
	if err := validateInterval(cfg.Probe.IntervalSeconds); err != nil {
		return fmt.Errorf("%w: %s", ErrChannelMonitorV3InvalidConfig, err)
	}
	cfg.Probe.Endpoint = normalizeEndpoint(cfg.Probe.Endpoint)
	cfg.Probe.Provider = strings.TrimSpace(cfg.Probe.Provider)
	if cfg.Probe.Provider == "" {
		cfg.Probe.Provider = MonitorProviderAnthropic
	}
	cfg.Probe.PrimaryModel = strings.TrimSpace(cfg.Probe.PrimaryModel)
	cfg.Probe.ExtraModels = normalizeStringSet(cfg.Probe.ExtraModels)

	groups := make([]ChannelMonitorV3Group, 0, len(cfg.Groups))
	seen := make(map[int64]struct{}, len(cfg.Groups))
	for _, g := range cfg.Groups {
		if g.GroupID <= 0 {
			return fmt.Errorf("%w: group_id must be positive", ErrChannelMonitorV3InvalidConfig)
		}
		if _, dup := seen[g.GroupID]; dup {
			continue
		}
		seen[g.GroupID] = struct{}{}
		g.Models = normalizeStringSet(g.Models)
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].GroupID < groups[j].GroupID })
	cfg.Groups = groups
	return nil
}

// validateChannelMonitorV3Probe 校验探针身份。
//
// V3 的探针永远是主动探活（没有 quota 模式），所以 endpoint/provider/model 必填，
// 且 provider 必须真的支持探活（antigravity 没有 adapter）。
func validateChannelMonitorV3Probe(endpoint, provider, primaryModel string, intervalSeconds int) error {
	if err := validateProvider(provider); err != nil {
		return fmt.Errorf("%w: %s", ErrChannelMonitorV3InvalidConfig, err)
	}
	if !providerSupportsProbe(provider) {
		return fmt.Errorf("%w: provider %q does not support active probes",
			ErrChannelMonitorV3InvalidConfig, provider)
	}
	if err := validateEndpoint(endpoint); err != nil {
		return fmt.Errorf("%w: %s", ErrChannelMonitorV3InvalidConfig, err)
	}
	if strings.TrimSpace(primaryModel) == "" {
		return fmt.Errorf("%w: primary_model is required", ErrChannelMonitorV3InvalidConfig)
	}
	if err := validateInterval(intervalSeconds); err != nil {
		return fmt.Errorf("%w: %s", ErrChannelMonitorV3InvalidConfig, err)
	}
	return nil
}

// ChannelMonitorV3PostEnabled reports whether V3 owns the passive board.
func ChannelMonitorV3Enabled(cfg *ChannelMonitorV3Config) bool {
	return cfg != nil && cfg.Enabled
}
