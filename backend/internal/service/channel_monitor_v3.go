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
	//
	// 分钟与天共用同一条上限：90 天 = 129600 分钟。用户要的是「同一个窗口能换单位
	// 表达」，不是「分钟能看更久」。两套上限会让同一个 90 天在切换单位后变成非法值，
	// 这正是用户报的「单位切换后值被重置」的成因，所以只留一个物理上限。
	ChannelMonitorV3MaxCacheAverageWindowDays = 90
	// ChannelMonitorV3MaxCacheAverageWindowMinutes 分钟单位下的同一上限。
	ChannelMonitorV3MaxCacheAverageWindowMinutes = ChannelMonitorV3MaxCacheAverageWindowDays * 24 * 60
	// ChannelMonitorV3DefaultIntervalSeconds 未配置 interval 时的默认探针间隔。
	ChannelMonitorV3DefaultIntervalSeconds = 300
	// ChannelMonitorV3DisabledIntervalFallback V3 关掉后 runner 的轮询间隔。
	ChannelMonitorV3DisabledIntervalFallback = 60 * time.Second

	// ChannelMonitorV3WindowUnitMinute / Day 是缓存率均值窗口的两种单位。
	//
	// 存 value + unit 而不是统一存分钟：老字段 cache_average_window_days 只能按天
	// 表达，升级后必须在页面上原样显示「30 天」，让它变成「43200 分钟」是可以用但
	// 明显不是用户填过的值。
	ChannelMonitorV3WindowUnitMinute = "minute"
	ChannelMonitorV3WindowUnitDay    = "day"
)

// minutesPerDay 是窗口单位换算的唯一常数，避免各处各写一个 1440。
const minutesPerDay = 24 * 60

// ChannelMonitorV3CacheWindow 是缓存率均值窗口：数值 + 单位。
//
// 之所以不直接把窗口统一折算成分钟存，是因为 UI 必须能原样回显用户填的单位：
// 用户填「2 天」，再打开页面看到「2880 分钟」会以为配置被改了。
type ChannelMonitorV3CacheWindow struct {
	Value int    `json:"value"`
	Unit  string `json:"unit"`
}

// Minutes 把窗口折算成分钟（下游 SQL 与「是否小于一天」的判定都用它）。
func (w ChannelMonitorV3CacheWindow) Minutes() int {
	if w.Value <= 0 {
		return 0
	}
	if w.Unit == ChannelMonitorV3WindowUnitDay {
		return w.Value * minutesPerDay
	}
	return w.Value
}

// WholeDays 返回窗口的整天数；窗口不是整天数时返回 0。
//
// 日汇总表 usage_group_daily 的最小粒度就是一天（主键 group_id, day），所以只有
// 「整天数」的窗口能走那张表的快路径。90 小时这种窗口返回 0，调用方据此改走
// usage_logs 直扫 —— 取整到 4 天会让「90 小时」悄悄变成「96 小时」。
func (w ChannelMonitorV3CacheWindow) WholeDays() int {
	minutes := w.Minutes()
	if minutes <= 0 || minutes%minutesPerDay != 0 {
		return 0
	}
	return minutes / minutesPerDay
}

// LegacyDays 返回老字段 cache_average_window_days 该写的值。
//
// 老列带 CHECK (BETWEEN 1 AND 90)，且旧版本客户端只读这一列，所以分钟窗口也要
// 折算成一个能落库的天数（向上取整、至少 1 天），而不是写 0 触发约束错误。
func (w ChannelMonitorV3CacheWindow) LegacyDays() int {
	if days := w.WholeDays(); days > 0 {
		return days
	}
	minutes := w.Minutes()
	if minutes <= 0 {
		return ChannelMonitorV3DefaultCacheAverageWindowDays
	}
	days := (minutes + minutesPerDay - 1) / minutesPerDay
	if days < 1 {
		days = 1
	}
	return days
}

// CacheWindow 是 cfg.CacheAverageWindow* 的规范化读法。
//
// 老配置只有 cache_average_window_days：unit 为空、value 为 0，此时按「天」解释，
// 迁移前后的语义完全一致。
func (c ChannelMonitorV3Config) CacheWindow() ChannelMonitorV3CacheWindow {
	unit := strings.TrimSpace(c.CacheAverageWindowUnit)
	if unit == ChannelMonitorV3WindowUnitMinute || unit == ChannelMonitorV3WindowUnitDay {
		if c.CacheAverageWindowValue > 0 {
			return ChannelMonitorV3CacheWindow{Value: c.CacheAverageWindowValue, Unit: unit}
		}
	}
	days := c.CacheAverageWindowDays
	if days <= 0 {
		days = ChannelMonitorV3DefaultCacheAverageWindowDays
	}
	return ChannelMonitorV3CacheWindow{Value: days, Unit: ChannelMonitorV3WindowUnitDay}
}

// CacheAverageWindowMinutes 是看板查询实际使用的窗口分钟数。
func (c ChannelMonitorV3Config) CacheAverageWindowMinutes() int {
	minutes := c.CacheWindow().Minutes()
	if minutes <= 0 {
		return ChannelMonitorV3DefaultCacheAverageWindowDays * minutesPerDay
	}
	return minutes
}

// ChannelMonitorV3Group 是 V3 看板上的一个分组条目。
//
// Models 为空表示「该分组不按模型拆分」，只有 ProbeEnabled 的分组才会被主动探针填充。
//
// Probe 是该分组专属的探针身份。零值表示「继承配置根上的全局 Probe」——
// 升级前的配置只有全局那份，靠这条回落规则行为完全不变。每个分组的可用 key 和
// 模型本来就不同（一个 key 通吃所有分组时，只有碰巧支持全部模型的那把能跑），
// 所以真正上线多分组时必须能各配各的。
type ChannelMonitorV3Group struct {
	GroupID      int64                 `json:"group_id"`
	Enabled      bool                  `json:"enabled"`
	Models       []string              `json:"models"`
	ProbeEnabled bool                  `json:"probe_enabled"`
	Probe        ChannelMonitorV3Probe `json:"probe"`
	// APIKeySet 是响应侧派生字段：该分组是否配了自己的探针 key。不回传密文。
	APIKeySet bool `json:"api_key_set"`
	// ProbeInherited 是响应侧派生字段：该分组是否仍在继承全局探针身份。
	// 前端据此显示「继承全局配置」而不是让管理员以为这里已经配过了。
	ProbeInherited bool `json:"probe_inherited"`
}

// ResolveProbe 返回该分组实际生效的探针身份。
//
// 分组级配置存在（endpoint 与 primary_model 都有值）就整份用分组级的；否则回落到
// 全局那份。刻意不做字段级合并：一个分组配了 endpoint 但没配 key 却借用全局 key，
// 会去用别的分组的凭证打另一个上游，这种「半继承」在排查时会非常难解释。
func (g ChannelMonitorV3Group) ResolveProbe(global ChannelMonitorV3Probe) ChannelMonitorV3Probe {
	if !g.HasOwnProbe() {
		return global
	}
	return g.Probe
}

// HasOwnProbe 报告分组是否配了自己的探针身份。
//
// 判据是 endpoint + primary_model：这两个字段是「打哪个上游、打哪个模型」的最小
// 完整信息，缺任一都无法单独成一次探测。只判 key 会把「换了个 key 但没换 endpoint」
// 误判成继承，从而用全局 endpoint 打分组自己的 key。
func (g ChannelMonitorV3Group) HasOwnProbe() bool {
	return strings.TrimSpace(g.Probe.Endpoint) != "" && strings.TrimSpace(g.Probe.PrimaryModel) != ""
}

// EffectiveIntervalSeconds 返回该分组的探测间隔：自带配置优先，否则用全局。
func (g ChannelMonitorV3Group) EffectiveIntervalSeconds(global ChannelMonitorV3Probe) int {
	probe := g.ResolveProbe(global)
	if probe.IntervalSeconds > 0 {
		return probe.IntervalSeconds
	}
	return ChannelMonitorV3DefaultIntervalSeconds
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
	Version int                     `json:"version"`
	Enabled bool                    `json:"enabled"`
	Groups  []ChannelMonitorV3Group `json:"groups"`
	// Probe 是全局探针身份。它有两个作用：老配置（分组里没有 probe）的回落默认值，
	// 以及页面上「所有分组共用一个身份」时的唯一配置点。
	Probe ChannelMonitorV3Probe `json:"probe"`
	// CacheAverageWindowDays 是老字段，只为兼容旧客户端与旧数据保留。
	// 新代码一律读 CacheWindow()。
	CacheAverageWindowDays int `json:"cache_average_window_days"`
	// CacheAverageWindowValue / Unit 是窗口的权威表达（数值 + 分钟/天）。
	// 两者都为 0/空时回落到 CacheAverageWindowDays（按天解释）。
	CacheAverageWindowValue int       `json:"cache_average_window_value"`
	CacheAverageWindowUnit  string    `json:"cache_average_window_unit"`
	UpdatedAt               time.Time `json:"updated_at"`
	UpdatedBy               *int64    `json:"updated_by,omitempty"`

	// APIKeySet 是响应侧派生字段：全局探针 key 是否已配置。不回传密文本身。
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

	// GroupCacheRates 取分组最近 windowMinutes 分钟真实流量的缓存率均值。
	// 返回 map[group_id] = 缓存率（0..1）；没有真实流量可算均值的分组不出现在 map 里。
	//
	// 单位是分钟而不是天：窗口可以配成「30 分钟」，日汇总表表达不了这个粒度，
	// 实现必须据此在「日汇总快路径」与「usage_logs 直扫」之间选路。
	GroupCacheRates(ctx context.Context, groupIDs []int64, windowMinutes int) (map[int64]float64, error)

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
// plainAPIKey 非空时加密覆盖全局探针 key；groupAPIKeys 是「分组 id → 明文 key」，
// 语义相同但作用于分组级探针。空 map / 缺项表示保留已存的密文，这样设置页不必回填
// 明文，改一个分组的间隔也不会把其他分组的 key 抹掉。
func (s *ChannelMonitorV3Service) UpdateConfig(
	ctx context.Context,
	cfg ChannelMonitorV3Config,
	plainAPIKey string,
	groupAPIKeys map[int64]string,
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
		encrypted, encErr := s.encryptSecret(key)
		if encErr != nil {
			return nil, encErr
		}
		cfg.Probe.APIKeyEncrypted = encrypted
	case existing != nil:
		// 未填新 key：沿用已存的密文，避免把已配置的探针身份清空。
		cfg.Probe.APIKeyEncrypted = existing.Probe.APIKeyEncrypted
	}

	// 分组级 key 与全局同一套「空 = 保留」规则。按 group_id 对齐 existing，
	// 这样前端只回传它改过的那些分组的 key。
	existingKeys := map[int64]string{}
	if existing != nil {
		for _, g := range existing.Groups {
			existingKeys[g.GroupID] = g.Probe.APIKeyEncrypted
		}
	}
	for i := range cfg.Groups {
		group := &cfg.Groups[i]
		if plain := strings.TrimSpace(groupAPIKeys[group.GroupID]); plain != "" {
			encrypted, encErr := s.encryptSecret(plain)
			if encErr != nil {
				return nil, encErr
			}
			group.Probe.APIKeyEncrypted = encrypted
			continue
		}
		if group.HasOwnProbe() {
			group.Probe.APIKeyEncrypted = existingKeys[group.GroupID]
		} else {
			// 没配自己的身份就不该留着别人的密文：留着会让前端以为这组配过 key。
			group.Probe.APIKeyEncrypted = ""
		}
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

// encryptSecret 加密一条明文 key，供全局与分组级共用。
func (s *ChannelMonitorV3Service) encryptSecret(plain string) (string, error) {
	if s.encryptor == nil {
		return "", fmt.Errorf("%w: secret encryptor unavailable", ErrChannelMonitorV3InvalidConfig)
	}
	encrypted, err := s.encryptor.Encrypt(plain)
	if err != nil {
		return "", fmt.Errorf("encrypt channel monitor v3 api key: %w", err)
	}
	return encrypted, nil
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
	for i := range cfg.Groups {
		group := &cfg.Groups[i]
		group.APIKeySet = strings.TrimSpace(group.Probe.APIKeyEncrypted) != ""
		group.Probe.APIKeyEncrypted = ""
		if group.Probe.ExtraModels == nil {
			group.Probe.ExtraModels = []string{}
		}
		group.Models = append([]string{}, group.Models...)
		if group.Models == nil {
			group.Models = []string{}
		}
		group.ProbeInherited = !group.HasOwnProbe()
	}
	window := cfg.CacheWindow()
	cfg.CacheAverageWindowValue = window.Value
	cfg.CacheAverageWindowUnit = window.Unit
	cfg.CacheAverageWindowDays = window.LegacyDays()
	return cfg
}

// normalizeChannelMonitorV3Config 归一化并校验分组名单 / 窗口 / 间隔。
func normalizeChannelMonitorV3Config(cfg *ChannelMonitorV3Config) error {
	window, err := normalizeChannelMonitorV3CacheWindow(cfg)
	if err != nil {
		return err
	}
	cfg.CacheAverageWindowValue = window.Value
	cfg.CacheAverageWindowUnit = window.Unit
	cfg.CacheAverageWindowDays = window.LegacyDays()

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
		if err := normalizeChannelMonitorV3GroupProbe(&g); err != nil {
			return err
		}
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].GroupID < groups[j].GroupID })
	cfg.Groups = groups
	return nil
}

// normalizeChannelMonitorV3GroupProbe 归一化分组级探针身份。
//
// 完全空的 probe（老配置）保持零值，代表「继承全局」；一旦填了任何字段就必须是
// 一份完整的身份，否则会掉进「半继承」——用 A 组的 endpoint 配 B 组的 key。
func normalizeChannelMonitorV3GroupProbe(g *ChannelMonitorV3Group) error {
	probe := &g.Probe
	probe.Endpoint = normalizeEndpoint(probe.Endpoint)
	probe.Provider = strings.TrimSpace(probe.Provider)
	probe.PrimaryModel = strings.TrimSpace(probe.PrimaryModel)
	probe.ExtraModels = normalizeStringSet(probe.ExtraModels)

	if probe.Endpoint == "" && probe.PrimaryModel == "" {
		// 老配置：整份清空，明确表达「继承全局」。
		g.Probe = ChannelMonitorV3Probe{ExtraModels: []string{}}
		return nil
	}
	if probe.Provider == "" {
		probe.Provider = MonitorProviderAnthropic
	}
	if probe.IntervalSeconds <= 0 {
		probe.IntervalSeconds = ChannelMonitorV3DefaultIntervalSeconds
	}
	if err := validateChannelMonitorV3Probe(probe.Endpoint, probe.Provider, probe.PrimaryModel, probe.IntervalSeconds); err != nil {
		return fmt.Errorf("group %d probe: %w", g.GroupID, err)
	}
	return nil
}

// normalizeChannelMonitorV3CacheWindow 归一化并校验窗口的数值与单位。
//
// 两件事必须一起做，否则就是用户报的「切单位后值被重置」：
//  1. 只有老字段（value/unit 都为空）时按天解释 —— 老配置升级后语义不变；
//  2. 单位只接受 minute/day，数值上限按单位各自换算，不做「切换单位顺便改数值」。
func normalizeChannelMonitorV3CacheWindow(cfg *ChannelMonitorV3Config) (ChannelMonitorV3CacheWindow, error) {
	unit := strings.TrimSpace(cfg.CacheAverageWindowUnit)
	value := cfg.CacheAverageWindowValue
	if unit == "" && value == 0 {
		// 老配置：只有 cache_average_window_days。
		days := cfg.CacheAverageWindowDays
		if days <= 0 {
			days = ChannelMonitorV3DefaultCacheAverageWindowDays
		}
		if days > ChannelMonitorV3MaxCacheAverageWindowDays {
			return ChannelMonitorV3CacheWindow{}, fmt.Errorf(
				"%w: cache_average_window_days must be between 1 and %d",
				ErrChannelMonitorV3InvalidConfig, ChannelMonitorV3MaxCacheAverageWindowDays)
		}
		return ChannelMonitorV3CacheWindow{Value: days, Unit: ChannelMonitorV3WindowUnitDay}, nil
	}
	if unit == "" {
		unit = ChannelMonitorV3WindowUnitDay
	}
	if unit != ChannelMonitorV3WindowUnitMinute && unit != ChannelMonitorV3WindowUnitDay {
		return ChannelMonitorV3CacheWindow{}, fmt.Errorf(
			"%w: cache_average_window_unit must be %q or %q",
			ErrChannelMonitorV3InvalidConfig, ChannelMonitorV3WindowUnitMinute, ChannelMonitorV3WindowUnitDay)
	}
	if value <= 0 {
		return ChannelMonitorV3CacheWindow{}, fmt.Errorf(
			"%w: cache_average_window_value must be positive", ErrChannelMonitorV3InvalidConfig)
	}
	window := ChannelMonitorV3CacheWindow{Value: value, Unit: unit}
	if window.Minutes() > ChannelMonitorV3MaxCacheAverageWindowMinutes {
		// 上限只有一个物理值（90 天），按分钟表达就是 129600，避免「同样的窗口
		// 换个单位就非法」。
		return ChannelMonitorV3CacheWindow{}, fmt.Errorf(
			"%w: cache average window must not exceed %d days (%d minutes)",
			ErrChannelMonitorV3InvalidConfig,
			ChannelMonitorV3MaxCacheAverageWindowDays, ChannelMonitorV3MaxCacheAverageWindowMinutes)
	}
	return window, nil
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
