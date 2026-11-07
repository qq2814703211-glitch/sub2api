package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// models.dev 价格同步（ADR 0010）。
//
// 背景：models.dev 提供一份官方**参考价**，我们的 pricing.override_file 是
// 可热更新的**结算价**覆盖层（含倍率、活动、渠道成本）。把两者接起来做「实时
// 官方价」很自然，但语义不同 —— 无条件把官方价写进覆盖层会冲掉商业定价。
//
// 决定：同步自动把 models.dev 的价格写进 pricing.override_file 并触发热重载，
// 但只补/改「我们价表里还没有手动定价」的模型；任何已手工配置过的模型都不被
// 覆盖。每次写入在 model_pricing_sync_history 留一行记录。
//
// 「这个字段是谁写的」由变更史判定：某 (模型, 字段) 只要在变更史里出现过，
// 就说明它由上一轮同步写入，本轮可以跟随官方价更新；否则覆盖层里已有非零值
// 的字段视为运营手配价，一律不动。
//
// 优先级（brief §5.3，必须被测试钉死）：分组价卡（ChannelModelPricing）
//   > models.dev 同步来的覆盖层 > 内置兜底价。
//
// 这条不靠本文件的写入顺序保证，而是靠 ModelPricingResolver 的解析顺序：
// 分组价卡命中时根本不读全局覆盖层。
const (
	// ModelPricingSyncSourceModelsDev 是本同步任务写入变更记录时使用的来源标识。
	ModelPricingSyncSourceModelsDev = "models.dev"

	// modelPricingSyncRegistryURL 是 models.dev 的官方注册表地址。
	modelPricingSyncRegistryURL = "https://models.dev/api.json"

	// modelPricingSyncBodyLimit 限制注册表响应体大小，避免异常响应撑爆内存。
	modelPricingSyncBodyLimit = 64 << 20

	// modelPricingSyncDefaultInterval 是未配置 pricing.hash_check_interval_minutes
	// 时的兜底间隔，与 PricingService 的定时器保持一致。
	modelPricingSyncDefaultInterval = 10 * time.Minute

	// modelPricingSyncReasonMissing 表示该字段在我们价表里完全没有手动定价，
	// 本次由同步任务补上。
	modelPricingSyncReasonMissing = "无手动定价，按 models.dev 参考价补入"

	// modelPricingSyncReasonDrift 表示该字段由上一轮同步写入，官方参考价变了。
	modelPricingSyncReasonDrift = "上一轮同步写入的参考价发生变化"
)

// modelPricingSyncRegistryProvider 是 models.dev 注册表里的一个供应商条目。
// 我们只关心 models 下的 cost 块。
type modelPricingSyncRegistryProvider struct {
	ID     string                                     `json:"id"`
	Name   string                                     `json:"name"`
	Models map[string]modelPricingSyncRegistryModel   `json:"models"`
}

// modelPricingSyncRegistryModel 是 models.dev 里的单个模型。价格单位是
// USD / 1M tokens。
type modelPricingSyncRegistryModel struct {
	ID   string                    `json:"id"`
	Cost *modelPricingSyncCostBlob `json:"cost"`
}

// modelPricingSyncCostBlob 是 models.dev 的 cost 块（USD / 1M tokens）。
// 这里只取与我们价表字段一一对应的四项：输入、输出、缓存读、缓存写。
type modelPricingSyncCostBlob struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// modelPricingSyncChange 是一条待写入覆盖层的变更。
type modelPricingSyncChange struct {
	Model    string  `json:"model"`
	Field    string  `json:"field"`
	OldValue float64 `json:"old_value"`
	NewValue float64 `json:"new_value"`
	Reason   string  `json:"reason"`
}

// ModelPricingSyncHistory 是 model_pricing_sync_history 的一行。
type ModelPricingSyncHistory struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	Model     string    `json:"model" gorm:"column:model"`
	Field     string    `json:"field" gorm:"column:field"`
	OldValue  float64   `json:"old_value" gorm:"column:old_value"`
	NewValue  float64   `json:"new_value" gorm:"column:new_value"`
	Source    string    `json:"source" gorm:"column:source"`
	AppliedAt time.Time `json:"applied_at" gorm:"column:applied_at"`
	Reason    string    `json:"reason" gorm:"column:reason"`
}

// ModelPricingSyncHistoryFilter 是变更史的查询条件。
type ModelPricingSyncHistoryFilter struct {
	Model    string
	Source   string
	Field    string
	Page     int
	PageSize int
}

// ModelPricingSyncHistoryRepository 是变更史的存储接口。
type ModelPricingSyncHistoryRepository interface {
	// InsertBatch 写入一批变更记录。
	InsertBatch(ctx context.Context, rows []*ModelPricingSyncHistory) (int64, error)
	// List 分页查询变更史，按 applied_at 倒序。返回记录与总数。
	List(ctx context.Context, filter *ModelPricingSyncHistoryFilter) ([]*ModelPricingSyncHistory, int64, error)
	// ListSyncedFields 返回某个来源曾写入过的 (模型, 字段) 集合，用于判定字段
	// 归属：出现在这里的字段由同步任务维护，可以跟随官方价更新。
	ListSyncedFields(ctx context.Context, source string) (map[string]map[string]float64, error)
}

// ModelPricingSyncService 把 models.dev 的官方参考价同步进 pricing.override_file。
type ModelPricingSyncService struct {
	cfg      *ModelPricingSyncConfig
	pricing  *PricingService
	history  ModelPricingSyncHistoryRepository
	fetcher  modelPricingSyncFetcher

	mu      sync.Mutex
	lastRun time.Time
	lastErr string
	lastApplied int

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// ModelPricingSyncConfig 是本服务的配置快照。
type ModelPricingSyncConfig struct {
	// OverrideFile 是 pricing.override_file 的路径，即同步写入目标。
	OverrideFile string
	// Interval 是定时同步间隔，取自 pricing.hash_check_interval_minutes。
	Interval time.Duration
	// RegistryURL 允许测试替换注册表地址。
	RegistryURL string
}

// modelPricingSyncFetcher 是抓取注册表的抽象。生产环境传 PricingRemoteClient
// （已带代理与连接池配置），测试传桩函数。
type modelPricingSyncFetcher interface {
	FetchPricingJSON(ctx context.Context, url string) ([]byte, error)
}

// modelPricingSyncHTTPFetcher 是没注入 fetcher 时的默认实现。
type modelPricingSyncHTTPFetcher struct{ client *http.Client }

func (f modelPricingSyncHTTPFetcher) FetchPricingJSON(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("models.dev registry returned HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, modelPricingSyncBodyLimit+1))
}

// NewModelPricingSyncService 创建同步服务。
func NewModelPricingSyncService(cfg *ModelPricingSyncConfig, pricing *PricingService, history ModelPricingSyncHistoryRepository, fetcher modelPricingSyncFetcher) *ModelPricingSyncService {
	if cfg == nil {
		cfg = &ModelPricingSyncConfig{}
	}
	if strings.TrimSpace(cfg.RegistryURL) == "" {
		cfg.RegistryURL = modelPricingSyncRegistryURL
	}
	if cfg.Interval < time.Minute {
		cfg.Interval = modelPricingSyncDefaultInterval
	}
	if fetcher == nil {
		fetcher = modelPricingSyncHTTPFetcher{client: &http.Client{Timeout: 60 * time.Second}}
	}
	return &ModelPricingSyncService{
		cfg:     cfg,
		pricing: pricing,
		history: history,
		fetcher: fetcher,
		stopCh:  make(chan struct{}),
	}
}

// SyncEnabled 报告同步是否可用：没有配置 override_file 就没有写入目标，
// 此时同步任务不启动，避免无意义的外网轮询。
func (s *ModelPricingSyncService) SyncEnabled() bool {
	return s != nil && s.cfg != nil && strings.TrimSpace(s.cfg.OverrideFile) != ""
}

// Start 启动定时同步。override_file 未配置时直接返回。
func (s *ModelPricingSyncService) Start() {
	if !s.SyncEnabled() {
		slog.Info("model pricing sync disabled: pricing.override_file is empty")
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 启动时先跑一轮，让新建部署尽快拿到官方价；失败不影响进程。
		if _, err := s.Sync(context.Background()); err != nil {
			slog.Warn("initial model pricing sync failed", "error", err)
		}
		ticker := time.NewTicker(s.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := s.Sync(context.Background()); err != nil {
					slog.Warn("model pricing sync failed", "error", err)
				}
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停止定时同步。
func (s *ModelPricingSyncService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

// Status 返回最近一次同步的状态，供管理端展示。
func (s *ModelPricingSyncService) Status() map[string]any {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"enabled":          s.SyncEnabled(),
		"last_run":         s.lastRun,
		"last_error":       s.lastErr,
		"last_applied":     s.lastApplied,
		"interval_seconds": int(s.cfg.Interval.Seconds()),
		"override_file":    s.cfg.OverrideFile,
	}
}

// Sync 执行一轮同步：拉注册表 → 算差异 → 原子写覆盖层 → 触发热重载 → 记变更史。
// 返回本次实际应用的变更条数。
//
// 失败保留上一份数据：拉不到 / 解析不出参考价时直接返回错误，完全不碰覆盖层。
func (s *ModelPricingSyncService) Sync(ctx context.Context) (int, error) {
	if !s.SyncEnabled() {
		return 0, fmt.Errorf("model pricing sync requires pricing.override_file")
	}
	registry, err := s.fetchRegistry(ctx)
	if err != nil {
		s.recordRun(err, 0)
		return 0, err
	}
	reference := buildModelPricingSyncReference(registry)
	if len(reference) == 0 {
		err := fmt.Errorf("models.dev registry contains no usable pricing")
		s.recordRun(err, 0)
		return 0, err
	}

	existing, err := s.loadOverrideEntries()
	if err != nil {
		s.recordRun(err, 0)
		return 0, fmt.Errorf("read override file: %w", err)
	}
	syncedFields, err := s.loadSyncedFields(ctx)
	if err != nil {
		// 变更史读不到时按「全部都是手动定价」处理：宁可不写，也不覆盖手配价。
		s.recordRun(err, 0)
		return 0, fmt.Errorf("read pricing sync history: %w", err)
	}

	changes := computeModelPricingSyncChanges(existing, reference, syncedFields)
	if len(changes) == 0 {
		s.recordRun(nil, 0)
		return 0, nil
	}

	merged := applyModelPricingSyncChanges(existing, changes)
	if err := s.writeOverrideFile(merged); err != nil {
		s.recordRun(err, 0)
		return 0, err
	}
	// 写完立刻走 PricingService 已有的文件指纹热重载路径，不需要重启进程。
	if s.pricing != nil {
		s.pricing.reloadIfCustomFilesChanged()
	}
	s.recordHistory(ctx, changes)
	s.recordRun(nil, len(changes))
	slog.Info("model pricing sync applied", "changes", len(changes), "models", countSyncModels(changes))
	return len(changes), nil
}

func (s *ModelPricingSyncService) recordRun(err error, applied int) {
	s.mu.Lock()
	s.lastRun = time.Now().UTC()
	if err != nil {
		s.lastErr = err.Error()
	} else {
		s.lastErr = ""
		s.lastApplied = applied
	}
	s.mu.Unlock()
}

func (s *ModelPricingSyncService) recordHistory(ctx context.Context, changes []modelPricingSyncChange) {
	if s.history == nil || len(changes) == 0 {
		return
	}
	now := time.Now().UTC()
	rows := make([]*ModelPricingSyncHistory, 0, len(changes))
	for _, change := range changes {
		rows = append(rows, &ModelPricingSyncHistory{
			Model:     change.Model,
			Field:     change.Field,
			OldValue:  change.OldValue,
			NewValue:  change.NewValue,
			Source:    ModelPricingSyncSourceModelsDev,
			AppliedAt: now,
			Reason:    change.Reason,
		})
	}
	if _, err := s.history.InsertBatch(ctx, rows); err != nil {
		// 记录失败不回滚覆盖层：价格已经生效，缺的是可观测性，不影响结算。
		slog.Warn("model pricing sync history insert failed", "rows", len(rows), "error", err)
	}
}

// loadSyncedFields 读取变更史里由同步任务写过的字段集合。
func (s *ModelPricingSyncService) loadSyncedFields(ctx context.Context) (map[string]map[string]float64, error) {
	if s.history == nil {
		return map[string]map[string]float64{}, nil
	}
	return s.history.ListSyncedFields(ctx, ModelPricingSyncSourceModelsDev)
}

// fetchRegistry 拉取 models.dev 注册表。
func (s *ModelPricingSyncService) fetchRegistry(ctx context.Context) (map[string]modelPricingSyncRegistryProvider, error) {
	endpoint := strings.TrimSpace(s.cfg.RegistryURL)
	if endpoint == "" {
		endpoint = modelPricingSyncRegistryURL
	}
	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("invalid models.dev registry url: %w", err)
	}
	body, err := s.fetcher.FetchPricingJSON(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch models.dev registry: %w", err)
	}
	if int64(len(body)) > modelPricingSyncBodyLimit {
		return nil, fmt.Errorf("models.dev registry exceeds %d bytes", modelPricingSyncBodyLimit)
	}
	var registry map[string]modelPricingSyncRegistryProvider
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("parse models.dev registry: %w", err)
	}
	if len(registry) == 0 {
		return nil, fmt.Errorf("models.dev registry is empty")
	}
	return registry, nil
}

// modelPricingSyncReferenceEntry 是某个模型在 models.dev 里的参考价（每 token，USD）。
type modelPricingSyncReferenceEntry struct {
	Provider string
	PerToken map[string]float64
}

func (e modelPricingSyncReferenceEntry) hasAnyPrice() bool {
	for _, price := range e.PerToken {
		if price > 0 {
			return true
		}
	}
	return false
}

// modelPricingSyncFields 是参与同步的字段，顺序固定以保证变更史可读。
var modelPricingSyncFields = []string{
	"input_cost_per_token",
	"output_cost_per_token",
	"cache_read_input_token_cost",
	"cache_creation_input_token_cost",
}

// modelPricingSyncOfficialProviders 是采信为「官方价」的 models.dev 供应商。
//
// models.dev 的注册表里有 200+ 个供应商，绝大多数是第三方转售/聚合网关
// （kenari、auriko、tokengo、lowrouter、above…），它们报的价是二手价，不是
// 官方价。不过滤的话，按字母序「先出现者胜」会把转售价当成官方价写进覆盖层。
//
// 这里只保留各家的第一方条目；区域镜像（alibaba-cn、moonshotai-cn、minimax-cn）
// 与官方 global 条目同价，不重复采信；coding-plan / token-plan 是套餐价，
// 不是按 token 的 API 价，一律排除。
var modelPricingSyncOfficialProviders = []string{
	"openai",
	"anthropic",
	"google",
	"deepseek",
	"xai",
	"mistral",
	"moonshotai",
	"zhipuai",
	"alibaba",
	"minimax",
	"meta",
	"cohere",
	"amazon-bedrock",
	"azure",
}

// buildModelPricingSyncReference 把注册表拍平成「模型名 → 参考价」，单位从
// models.dev 的 USD / 1M tokens 换算成与覆盖层一致的 USD / token。
//
// 只采信 modelPricingSyncOfficialProviders 里的第一方供应商。同名模型出现在
// 多个官方供应商下时以先出现者为准（供应商与模型都先排序，保证结果确定性）。
func buildModelPricingSyncReference(registry map[string]modelPricingSyncRegistryProvider) map[string]modelPricingSyncReferenceEntry {
	providers := make([]string, 0, len(modelPricingSyncOfficialProviders))
	for _, id := range modelPricingSyncOfficialProviders {
		if _, ok := registry[id]; ok {
			providers = append(providers, id)
		}
	}
	sort.Strings(providers)

	reference := make(map[string]modelPricingSyncReferenceEntry)
	for _, providerID := range providers {
		provider := registry[providerID]
		models := make([]string, 0, len(provider.Models))
		for id := range provider.Models {
			models = append(models, id)
		}
		sort.Strings(models)
		for _, modelID := range models {
			entry := provider.Models[modelID]
			if entry.Cost == nil {
				continue
			}
			key := normalizeModelPricingSyncModelName(modelID)
			if key == "" {
				continue
			}
			if _, exists := reference[key]; exists {
				continue
			}
			priced := modelPricingSyncReferenceEntry{
				Provider: providerID,
				PerToken: map[string]float64{
					"input_cost_per_token":            entry.Cost.Input / 1e6,
					"output_cost_per_token":           entry.Cost.Output / 1e6,
					"cache_read_input_token_cost":     entry.Cost.CacheRead / 1e6,
					"cache_creation_input_token_cost": entry.Cost.CacheWrite / 1e6,
				},
			}
			if !priced.hasAnyPrice() {
				continue
			}
			reference[key] = priced
		}
	}
	return reference
}

// computeModelPricingSyncChanges 计算需要写入的变更（ADR 0010 的核心判定）。
//
// 判定是**模型级**的：ADR 原文是「只写我们的价表里还没有手动定价的模型；任何
// 已手工配置过的模型都不被覆盖」。所以只要覆盖层里该模型有任何一个字段是非零、
// 且不是同步任务写的，就认为这个模型是运营手配价 —— 整个模型跳过，连它缺的
// 字段也不补。半配价的模型（例如只配了输入价）同样算手配，否则给它补上输出价
// 会把运营刻意留空的商业口径填成官方价。
//
// 只有「整条记录都由同步任务维护」的模型才会被更新：把官方价的变化跟进去。
func computeModelPricingSyncChanges(
	existing map[string]json.RawMessage,
	reference map[string]modelPricingSyncReferenceEntry,
	syncedFields map[string]map[string]float64,
) []modelPricingSyncChange {
	var changes []modelPricingSyncChange
	models := make([]string, 0, len(reference))
	for model := range reference {
		models = append(models, model)
	}
	sort.Strings(models)

	for _, model := range models {
		priced := reference[model]
		currentFields := decodeModelPricingSyncEntry(existing[model])
		if modelHasManualPricing(currentFields, syncedFields[model]) {
			// 已手工配置过：绝不覆盖，整个模型跳过。
			continue
		}
		for _, field := range modelPricingSyncFields {
			newValue, ok := priced.PerToken[field]
			if !ok || newValue <= 0 {
				continue
			}
			oldValue := currentFields[field]
			if oldValue == newValue {
				continue
			}
			reason := modelPricingSyncReasonMissing
			if oldValue > 0 {
				reason = modelPricingSyncReasonDrift
			}
			changes = append(changes, modelPricingSyncChange{
				Model:    model,
				Field:    field,
				OldValue: oldValue,
				NewValue: newValue,
				Reason:   reason,
			})
		}
	}
	return changes
}

// modelHasManualPricing 报告覆盖层里该模型是否有运营手配的字段。
//
// 判据是「当前值与同步任务上次写入的值不同」，而不是「字段是否出现在同步史里」。
// 只比对字段名的做法会漏掉最常见的一种顺序：同步先写入了某个模型，之后运营才
// 手动改价。此时该字段既非零、又出现在同步史里，旧规则就会把它判成同步所有，
// 下一轮静默覆盖掉运营的手工价。
//
// synced 记录的是「上次同步写进去的值」；字段不在其中，或其中记的值与当前值不等，
// 都说明当前值是别人写的。
func modelHasManualPricing(fields map[string]float64, synced map[string]float64) bool {
	for field, value := range fields {
		if value == 0 {
			continue
		}
		lastSynced, syncedBefore := synced[field]
		if syncedBefore && lastSynced == value {
			// 与上次同步写入的值一致：这是同步自己写的，可以继续跟随官方价。
			continue
		}
		return true
	}
	return false
}

// decodeModelPricingSyncEntry 把覆盖层里某个模型的原始 JSON 解成字段表。
func decodeModelPricingSyncEntry(raw json.RawMessage) map[string]float64 {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	out := make(map[string]float64, len(fields))
	for key, value := range fields {
		if number, ok := value.(float64); ok {
			out[key] = number
		}
	}
	return out
}

// applyModelPricingSyncChanges 把变更合并进覆盖层条目，返回新的覆盖层对象。
// 已有条目的其他字段（含运营手配的倍率、阶梯开关等）原样保留。
func applyModelPricingSyncChanges(existing map[string]json.RawMessage, changes []modelPricingSyncChange) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(existing)+16)
	for model, raw := range existing {
		out[model] = raw
	}
	grouped := make(map[string]map[string]any)
	for _, change := range changes {
		fields, ok := grouped[change.Model]
		if !ok {
			fields = make(map[string]any)
			if raw, exists := out[change.Model]; exists {
				var decoded map[string]any
				if err := json.Unmarshal(raw, &decoded); err == nil && decoded != nil {
					fields = decoded
				}
			}
			grouped[change.Model] = fields
		}
		fields[change.Field] = change.NewValue
	}
	for model, fields := range grouped {
		encoded, err := json.Marshal(fields)
		if err != nil {
			continue
		}
		out[model] = encoded
	}
	return out
}

// loadOverrideEntries 读取当前覆盖层文件。文件不存在视为空覆盖层。
func (s *ModelPricingSyncService) loadOverrideEntries() (map[string]json.RawMessage, error) {
	path := strings.TrimSpace(s.cfg.OverrideFile)
	if path == "" {
		return nil, fmt.Errorf("pricing.override_file is empty")
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("override file is not a JSON object: %w", err)
	}
	if entries == nil {
		entries = map[string]json.RawMessage{}
	}
	return entries, nil
}

// writeOverrideFile 原子写入覆盖层：先写同目录临时文件并 fsync，再 rename 覆盖。
// 任何一步失败都保留原文件 —— 绝不能写坏现有覆盖层。
func (s *ModelPricingSyncService) writeOverrideFile(entries map[string]json.RawMessage) error {
	path := strings.TrimSpace(s.cfg.OverrideFile)
	if path == "" {
		return fmt.Errorf("pricing.override_file is empty")
	}
	// 写入前校验：确保编码出的内容是可解析的 JSON 对象。
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode override file: %w", err)
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &check); err != nil {
		return fmt.Errorf("encode override file produced invalid JSON: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create override dir: %w", err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp override file: %w", err)
	}
	tempPath := temp.Name()
	// 失败路径统一清理临时文件；rename 成功后再清理是 no-op。
	defer func() { _ = os.Remove(tempPath) }()

	if _, err := temp.Write(encoded); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temp override file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temp override file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp override file: %w", err)
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return fmt.Errorf("chmod temp override file: %w", err)
	}
	// rename 在同一文件系统内是原子的替换。
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace override file: %w", err)
	}
	return nil
}

// normalizeModelPricingSyncModelName 统一模型名大小写（覆盖层按小写索引，
// 与 PricingService 的查找路径一致）。
func normalizeModelPricingSyncModelName(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func countSyncModels(changes []modelPricingSyncChange) int {
	seen := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		seen[change.Model] = struct{}{}
	}
	return len(seen)
}
