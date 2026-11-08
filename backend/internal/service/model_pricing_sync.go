package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
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

	// ModelPricingSyncSourceCNYOfficial 是人民币官方价写入变更记录时使用的来源标识。
	// 与 models.dev 分开记，才能按口径追溯「这个数字是谁写的」，也才能让
	// modelHasManualPricing 正确判定跨口径的字段归属。
	ModelPricingSyncSourceCNYOfficial = "cny-official"

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
	ID     string                                   `json:"id"`
	Name   string                                   `json:"name"`
	Models map[string]modelPricingSyncRegistryModel `json:"models"`
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
	// Source 是写入该变更的口径来源（models.dev / cny-official）。变更史按来源
	// 分列，才能追溯「这个数字是美元参考价还是人民币官方牌价」。
	Source string `json:"source"`
}

// source 返回变更的口径来源，缺省按美元档处理（兼容未填 Source 的历史路径）。
func (c modelPricingSyncChange) source() string {
	if strings.TrimSpace(c.Source) == "" {
		return ModelPricingSyncSourceModelsDev
	}
	return c.Source
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
	settings cnyOfficialSettingsReader

	mu             sync.Mutex
	lastRun        time.Time
	lastErr        string
	lastApplied    int
	cnyReport      *CNYOfficialSyncReport
	cnyLastErr     string
	cnyLastApplied int

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
	// SettingsReader 读取 pricing.cny_official。为 nil 时人民币口径整体关闭，
	// 行为与未引入该功能前完全一致（只有 usd 一档）。
	SettingsReader cnyOfficialSettingsReader
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
		cfg:      cfg,
		pricing:  pricing,
		history:  history,
		fetcher:  fetcher,
		settings: cfg.SettingsReader,
		stopCh:   make(chan struct{}),
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
//
// 三档口径的可观测性都在这里：cny_official 子对象给出解析报告（哪些阶梯只
// 表达了第 1 档、哪些倍率冲突、哪些国模因无人民币价被跳过），time_pricing 给出
// 已平移时区的分时价。运营不读日志也能核对。
func (s *ModelPricingSyncService) Status() map[string]any {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := map[string]any{
		"enabled":          s.SyncEnabled(),
		"last_run":         s.lastRun,
		"last_error":       s.lastErr,
		"last_applied":     s.lastApplied,
		"interval_seconds": int(s.cfg.Interval.Seconds()),
		"override_file":    s.cfg.OverrideFile,
		// 口径优先级固定，前端按这个顺序展示。
		"tier_priority": []string{
			ModelPricingTierUSD,
			ModelPricingTierCNYOfficial,
			ModelPricingTierManual,
		},
	}
	cnyStatus := map[string]any{
		"enabled":         s.settings != nil,
		"last_error":      s.cnyLastErr,
		"last_applied":    s.cnyLastApplied,
		"source_url":      cnyOfficialDefaultSourceURL,
		"site_timezone":   cnyOfficialSiteTimezone,
		"national_vendor": sortedStrings(cnyOfficialProviderWhitelist),
	}
	if s.cnyReport != nil {
		cnyStatus["report"] = s.cnyReport
	}
	status["cny_official"] = cnyStatus
	return status
}

// sortedStrings 返回稳定顺序的键列表，让 Status 输出可被前端/脚本直接比对。
func sortedStrings[V any](in map[string]V) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Sync 执行一轮同步：拉注册表 → 算差异 → 原子写覆盖层 → 触发热重载 → 记变更史。
// 返回本次实际应用的变更条数。
//
// 失败保留上一份数据：拉不到 / 解析不出参考价时直接返回错误，完全不碰覆盖层。
//
// 三档口径（usd < cny-official < manual）在这里合流：
//   - usd 档：models.dev 参考价，只负责**非国模**模型；
//   - cny-official 档：人民币官方牌价，负责国模，命中时覆盖 usd 档的同一字段；
//   - manual 档：覆盖层里已手工配置的模型整条跳过，任何档都不得覆盖。
//
// 国模（人民币厂商白名单 / CNY 计价）的模型只由 cny 档处理。没有人民币价的国模
// 一律不写，绝不回退 models.dev 的美元价 —— 那会把这个模型按美元数字收费，
// 比「没配价」严重得多。同理，cny 档开启但本轮抓取失败时整轮放弃：只跑 usd 档
// 会把已按人民币配好的国模改回美元数字。
func (s *ModelPricingSyncService) Sync(ctx context.Context) (int, error) {
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
	syncedFields, cnyWrittenHist, err := s.loadSyncedFieldsWithCNY(ctx)
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

// recordCNYRun 记录人民币口径这一档的独立状态。report 为 nil 时保留上一份解析
// 报告（不因为一次失败就丢掉可观测信息）。
func (s *ModelPricingSyncService) recordCNYRun(err error, report *CNYOfficialSyncReport, applied int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.cnyLastErr = err.Error()
		return
	}
	s.cnyLastErr = ""
	s.cnyLastApplied = applied
	if report != nil {
		s.cnyReport = report
	}
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
			Source:    change.source(),
			AppliedAt: now,
			Reason:    change.Reason,
		})
	}
	if _, err := s.history.InsertBatch(ctx, rows); err != nil {
		// 记录失败不回滚覆盖层：价格已经生效，缺的是可观测性，不影响结算。
		slog.Warn("model pricing sync history insert failed", "rows", len(rows), "error", err)
	}
}

// loadSyncedFields 读取变更史里由各口径同步任务写过的字段集合。
//
// 两个来源都要读：字段归属判定必须知道「当前值是哪个口径写的」。只读 models.dev
// 会把上一轮人民币档写入的值当成运营手配，从此再也不敢更新；反过来只读人民币档
// 会让美元档的字段被误判成手配。
//
// 同一 (模型, 字段) 在两个来源都出现过时以人民币档为准：那是后写入的更高优先级
// 口径（usd < cny-official）。仓储的 ListSyncedFields 只按来源取各自最新一行，
// 拿不到跨来源的先后顺序，所以这里用「高优先级覆盖低优先级」这个确定性规则，
// 而不是猜时间。
func (s *ModelPricingSyncService) loadSyncedFields(ctx context.Context) (map[string]map[string]float64, error) {
	merged, _, err := s.loadSyncedFieldsWithCNY(ctx)
	return merged, err
}

// loadSyncedFieldsWithCNY 同 loadSyncedFields，额外返回「历史上被人民币档写入过」
// 的模型集合。覆盖层打 cny_official 标记时需要它：只有真正写过人民币值的条目
// 才能标成人民币口径，否则值与标记错配（标着人民币、值还是美元）。
func (s *ModelPricingSyncService) loadSyncedFieldsWithCNY(ctx context.Context) (map[string]map[string]float64, map[string]struct{}, error) {
	merged := map[string]map[string]float64{}
	cnyWritten := map[string]struct{}{}
	if s.history == nil {
		return merged, cnyWritten, nil
	}
	for _, source := range []string{ModelPricingSyncSourceModelsDev, ModelPricingSyncSourceCNYOfficial} {
		fields, err := s.history.ListSyncedFields(ctx, source)
		if err != nil {
			return nil, nil, err
		}
		for model, perField := range fields {
			target, ok := merged[model]
			if !ok {
				target = map[string]float64{}
				merged[model] = target
			}
			for field, value := range perField {
				target[field] = value
			}
			if source == ModelPricingSyncSourceCNYOfficial && len(perField) > 0 {
				cnyWritten[model] = struct{}{}
			}
		}
	}
	return merged, cnyWritten, nil
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
//
// 入参 skip 里的模型无论字段状态如何都整条跳过（人民币档用来排除国模：国模
// 的价格只能由 cny-official 档给，美元档不得插手）。
func computeModelPricingSyncChanges(
	existing map[string]json.RawMessage,
	reference map[string]modelPricingSyncReferenceEntry,
	syncedFields map[string]map[string]float64,
	skip map[string]struct{},
) []modelPricingSyncChange {
	var changes []modelPricingSyncChange
	models := make([]string, 0, len(reference))
	for model := range reference {
		models = append(models, model)
	}
	sort.Strings(models)

	for _, model := range models {
		if _, skipped := skip[model]; skipped {
			continue
		}
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
				Source:   ModelPricingSyncSourceModelsDev,
			})
		}
	}
	return changes
}

// mergeModelPricingTierChanges 按 usd < cny-official < manual 合并两档同步价。
//
// 语义（这是本 lane 的核心，必须被测试钉死）：
//
//  1. manual 最高。覆盖层里该模型任一字段「非零且与上次同步写入值不同」→ 整条
//     跳过，两档都不写。manual_overrides 命中的模型同样整条跳过。
//  2. cny-official 次之，只管国模：白名单厂商 / CNY 计价厂商下的模型。这些模型
//     **只**由人民币档处理，美元档完全不碰 —— 没有人民币价就跳过，绝不回退美元。
//  3. usd 最低，管非国模。它与人民币档没有交集（国模已被排除），所以「cny 覆盖
//     usd」在实际数据里表现为「国模只出现人民币数字，非国模只出现美元数字」。
//     即便如此仍显式按 cny 后写覆盖 usd 处理，防止两档因厂商白名单扩充而同时
//     命中同一模型时出现顺序不确定。
//
// 返回的变更里每条都带 Source，变更史据此可追溯口径。
func mergeModelPricingTierChanges(
	existing map[string]json.RawMessage,
	reference map[string]modelPricingSyncReferenceEntry,
	dataset *cnyOfficialDataset,
	syncedFields map[string]map[string]float64,
) []modelPricingSyncChange {
	// 国模名单：无论人民币档是否命中，美元档都不得为这些模型写价。
	// 运营手配的模型一并排除：手配价常用来给「官方源没有人民币牌价」的模型
	// 定本位币价，这类模型未必在 National 里，若让美元档继续写就会把手配价改回美元。
	skipForUSD := map[string]struct{}{}
	if dataset != nil {
		for model := range dataset.National {
			skipForUSD[model] = struct{}{}
		}
		for model := range dataset.ManualOverrides {
			skipForUSD[model] = struct{}{}
		}
	}
	changes := computeModelPricingSyncChanges(existing, reference, syncedFields, skipForUSD)
	if dataset == nil {
		return changes
	}

	// 人民币档：后写覆盖前写，实现 cny-official > usd。
	cnyChanges := computeCNYOfficialSyncChanges(existing, dataset, syncedFields)
	if len(cnyChanges) == 0 {
		return changes
	}
	// 同一 (模型, 字段) 的美元变更被人民币变更顶掉。
	type modelField struct{ model, field string }
	replaced := make(map[modelField]struct{}, len(cnyChanges))
	for _, change := range cnyChanges {
		replaced[modelField{change.Model, change.Field}] = struct{}{}
	}
	filtered := changes[:0]
	for _, change := range changes {
		if _, ok := replaced[modelField{change.Model, change.Field}]; ok {
			continue
		}
		filtered = append(filtered, change)
	}
	return append(filtered, cnyChanges...)
}

// computeCNYOfficialSyncChanges 计算人民币官方价档的变更。
//
// 只处理 dataset 里真正解析出人民币价的模型；国模里没有人民币价的模型既不在
// Pricing 里也不产生变更 —— 跳过即「不回退美元」的实现。
func computeCNYOfficialSyncChanges(
	existing map[string]json.RawMessage,
	dataset *cnyOfficialDataset,
	syncedFields map[string]map[string]float64,
) []modelPricingSyncChange {
	if dataset == nil {
		return nil
	}
	// 模型集 = 官方人民币价覆盖到的模型 ∪ 运营手配的模型。
	// 后者必须并入：手配价常用来给「官方数据源没有人民币牌价」的模型定价
	// （典型：只有境外厂商美元价的 deepseek-v4.1-flash-1017）。只看官方表会
	// 让这些手工配置永远不生效。
	// 注意：键一律用裸模型名。manual_overrides 允许 "<vendor>/<model>" 写法，
	// 但覆盖层与计费查询都用裸模型名，带前缀写进去等于写了一条查不到的孤儿条目。
	modelSet := make(map[string]struct{}, len(dataset.Pricing)+len(dataset.ManualOverrides))
	for model := range dataset.Pricing {
		modelSet[model] = struct{}{}
	}
	for key := range dataset.ManualOverrides {
		if model := cnyOfficialNormalizeManualKey(key); model != "" {
			modelSet[model] = struct{}{}
		}
	}
	if len(modelSet) == 0 {
		return nil
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)

	var changes []modelPricingSyncChange
	for _, model := range models {
		priced, ok := dataset.pricing(model)
		// manual_overrides 命中：按运营配置的人民币价写入，优先级最高。
		currentFields := decodeModelPricingSyncEntry(existing[model])
		if manual, hit := dataset.manualPricing(model, providerOf(priced)); hit {
			changes = append(changes, cnyOfficialManualChanges(model, manual.PerToken, manual.LongContext, currentFields)...)
			continue
		}
		if !ok || priced == nil {
			continue
		}
		if modelHasManualPricing(currentFields, syncedFields[model]) {
			// 运营在覆盖层里手配的价：永不被覆盖。
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
			changes = append(changes, modelPricingSyncChange{
				Model:    model,
				Field:    field,
				OldValue: oldValue,
				NewValue: newValue,
				Reason:   cnyOfficialReason(priced.Provider, oldValue),
				Source:   ModelPricingSyncSourceCNYOfficial,
			})
		}

		// 长上下文阶梯：覆盖层只有「单阈值 + 两侧倍率」三字段，整体覆盖写入。
		changes = append(changes, cnyOfficialLadderChanges(model, priced.LongContext, currentFields)...)
	}
	return changes
}

// cnyOfficialNormalizeManualKey 归一 manual_overrides 的键（小写 + 去空格）。
//
// 关键：**原样保留**，不剥任何前缀。覆盖层的键就是模型请求名，而真实模型名
// 本身可能带斜杠（生产覆盖层里就有 `siliconflow/deepseek-v3.2` 这类真实条目）。
// 早先版本用 LastIndex("/") 无条件剥前缀，把 `siliconflow/deepseek-v3.2`
// 剥成 `deepseek-v3.2` —— 既写错了位置，又把同名的另一条真模型覆盖掉
// （实测：`siliconflow/deepseek-v3.2` 从 0.27 被改成 manual 里的 55）。
//
// 计费侧查找（buildModelLookupCandidates）本来就会同时尝试全名和 lastSegment，
// 所以裸名请求能命中裸名条目、带斜杠请求能命中带斜杠条目，两边都不需要剥。
func cnyOfficialNormalizeManualKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// cnyOfficialManualLongContext 把手工配置的长上下文三项折成覆盖层阶梯。
//
// 返回 nil 表示不开长上下文：阈值未填、或两侧倍率都不大于 1 时都没意义
// （计费侧同样以「倍率 <= 1」判定不启用）。倍率留空按 1 处理，只有一侧配置时
// 另一侧保持 1 —— 与 official 数据的写法一致。
func cnyOfficialManualLongContext(price CNYOfficialManualPrice) *cnyOfficialLongContext {
	if price.LongContextThreshold <= 0 {
		return nil
	}
	inputMult := price.LongContextInputMultiplier
	outputMult := price.LongContextOutputMultiplier
	if inputMult <= 0 {
		inputMult = 1
	}
	if outputMult <= 0 {
		outputMult = 1
	}
	if inputMult <= 1 && outputMult <= 1 {
		return nil
	}
	return &cnyOfficialLongContext{
		Threshold:        price.LongContextThreshold,
		InputMultiplier:  inputMult,
		OutputMultiplier: outputMult,
	}
}

// providerOf 安全取出 priced 的厂商名，priced 为 nil 时返回空串。
func providerOf(priced *cnyOfficialPricing) string {
	if priced == nil {
		return ""
	}
	return priced.Provider
}

// cnyOfficialManualChanges 按运营手配价生成变更。
//
// manual 是最高优先级一档：只要配置了，就按这份价格写覆盖层，不看官方人民币价
// 是否有值。这样即使某个模型在官方数据源里没有人民币牌价（只有境外厂商的美元价），
// 运营也能把它改成本位币计价。
func cnyOfficialManualChanges(model string, perToken map[string]float64, longContext *cnyOfficialLongContext, current map[string]float64) []modelPricingSyncChange {
	changes := make([]modelPricingSyncChange, 0, len(modelPricingSyncFields)+3)
	for _, field := range modelPricingSyncFields {
		newValue, ok := perToken[field]
		if !ok || newValue <= 0 {
			continue
		}
		oldValue := current[field]
		if oldValue == newValue {
			continue
		}
		changes = append(changes, modelPricingSyncChange{
			Model:    model,
			Field:    field,
			OldValue: oldValue,
			NewValue: newValue,
			Reason:   "按运营手工配置的人民币价写入（manual 优先级最高）",
			Source:   ModelPricingSyncSourceCNYOfficial,
		})
	}
	// 长上下文阶梯：三字段成组写，与官方档共用同一套逻辑（只写阈值不写倍率，
	// 等于「超过阈值后价格不变」）。
	changes = append(changes, cnyOfficialLadderChanges(model, longContext, current)...)
	return changes
}

// cnyOfficialReason 生成人民币档的变更原因，带上厂商便于核对牌价出处。
func cnyOfficialReason(provider string, oldValue float64) string {
	if oldValue > 0 {
		return fmt.Sprintf("上一轮同步写入的价发生变化，按 %s 官方人民币牌价更新", provider)
	}
	return fmt.Sprintf("无手动定价，按 %s 官方人民币牌价补入", provider)
}

// cnyOfficialLadderChanges 生成长上下文阶梯的三条变更。
//
// 阶梯三字段必须成组写：只写阈值不写倍率，等于「超过阈值后价格不变」，
// 会让长上下文照低价计费（历史上真实的漏收形态）。
func cnyOfficialLadderChanges(model string, ladder *cnyOfficialLongContext, current map[string]float64) []modelPricingSyncChange {
	if ladder == nil || ladder.Threshold <= 0 {
		return nil
	}
	wanted := []struct {
		field string
		value float64
	}{
		{cnyOfficialFieldThreshold, float64(ladder.Threshold)},
		{cnyOfficialFieldInputMult, ladder.InputMultiplier},
		{cnyOfficialFieldOutputMult, ladder.OutputMultiplier},
	}
	changes := make([]modelPricingSyncChange, 0, len(wanted))
	for _, item := range wanted {
		oldValue := current[item.field]
		if oldValue == item.value {
			continue
		}
		changes = append(changes, modelPricingSyncChange{
			Model:    model,
			Field:    item.field,
			OldValue: oldValue,
			NewValue: item.value,
			Reason: fmt.Sprintf("按官方人民币牌价折算长上下文阶梯（%d 档，本版只表达第 1 档阈值 %d）",
				ladder.TierCount, ladder.Threshold),
			Source: ModelPricingSyncSourceCNYOfficial,
		})
	}
	return changes
}

// countSyncModelsBySource 统计某口径实际写入的模型数（按模型去重）。
func countSyncModelsBySource(changes []modelPricingSyncChange, source string) int {
	seen := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		if change.source() != source {
			continue
		}
		seen[change.Model] = struct{}{}
	}
	return len(seen)
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
//
// 只检查 modelPricingSyncFields 里的价格字段。覆盖层条目里混着大量非价格的数值
// 字段（long_context_input_cost_multiplier、long_context_input_token_threshold、
// 分时倍率等），它们永远不出现在同步史里，若一并参与判定，每个带长上下文的模型
// 都会被误判成「运营手配价」而整条跳过同步 —— 表现为条目被打上人民币标记、值却
// 停留在上一版美元价，模型广场显示的价格与官方牌价对不上。
func modelHasManualPricing(fields map[string]float64, synced map[string]float64) bool {
	for _, field := range modelPricingSyncFields {
		value := fields[field]
		if value == 0 {
			continue
		}
		lastSynced, syncedBefore := synced[field]
		if syncedBefore && modelPricingValueEqual(lastSynced, value) {
			// 与上次同步写入的值一致：这是同步自己写的，可以继续跟随官方价。
			continue
		}
		return true
	}
	return false
}

// modelPricingValueEqual 按相对容差比较两个价格，而不是直接 ==。
//
// 同步史里的值与覆盖层里的值经过不同路径落盘（数据库 numeric 往返、JSON 浮点
// 序列化），末位会有 1e-16 量级的差。直接比较会把「同步自己写的值」判成运营
// 手配价，导致这些模型永远不再跟随官方价更新 —— 症状是价格停在旧值且不报错。
// 价格数量级跨 1e-9..1e-4，用相对容差才能同时覆盖两端。
func modelPricingValueEqual(a, b float64) bool {
	if a == b {
		return true
	}
	diff := math.Abs(a - b)
	scale := math.Max(math.Abs(a), math.Abs(b))
	if scale == 0 {
		return diff == 0
	}
	return diff/scale < 1e-9
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

// cnyOfficialModelSet 返回本轮「按人民币官方价口径处理」的模型集合。
//
// 这个集合决定哪些覆盖层条目要打上 cny_official 标记。它必须来自数据源
// 本身（dataset.Pricing 的键）加上运营手配的模型，不能从「本轮产生了变更」反推：
// 已经同步过、价格没变的模型不产生 change，若按 change 判定标记，那些条目会
// 永远缺标记，进而在计费时被 DeepSeek 美元谷价兜底覆盖。
//
// 手配模型必须并入：模型广场的「官方参考价」直接读覆盖层，缺标记的条目会被
// 当成美元价展示，手配的人民币数字就白配了。
func cnyOfficialModelSet(dataset *cnyOfficialDataset) map[string]struct{} {
	if dataset == nil {
		return nil
	}
	if len(dataset.Pricing) == 0 && len(dataset.ManualOverrides) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(dataset.Pricing)+len(dataset.ManualOverrides))
	for model := range dataset.Pricing {
		out[model] = struct{}{}
	}
	for model := range dataset.ManualOverrides {
		out[model] = struct{}{}
	}
	return out
}

// cnyWrittenModelSet 返回「本条目的价格确实由人民币档写入过」的模型集合。
//
// 两个来源合并：本轮产生 cny-official 变更的模型，加上历史变更史里已有
// cny-official 写入记录的模型（后者覆盖「本轮价格没变、不产生 change」的情况，
// 否则这些条目的标记会丢失）。
func cnyWrittenModelSet(changes []modelPricingSyncChange, history map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(history)+8)
	for model := range history {
		out[model] = struct{}{}
	}
	for _, change := range changes {
		if change.source() != ModelPricingSyncSourceCNYOfficial {
			continue
		}
		out[change.Model] = struct{}{}
	}
	return out
}

// applyModelPricingSyncChanges 把变更合并进覆盖层条目，返回新的覆盖层对象。
// 已有条目的其他字段（含运营手配的倍率、阶梯开关等）原样保留。
func applyModelPricingSyncChanges(
	existing map[string]json.RawMessage,
	changes []modelPricingSyncChange,
	cnyModels map[string]struct{},
	cnyWrittenModels map[string]struct{},
) map[string]json.RawMessage {
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

	// 人民币官方价口径的条目必须带上 cny_official 标记：计费侧据此跳过
	// 「DeepSeek 一律强制回官方低谷美元价」的兜底改写，否则人民币价写入覆盖层
	// 后在计费时会被静默吞掉。
	//
	// 标记按「数据源里属于人民币口径的模型」整体置位，而不是按「本轮有变更」——
	// 已经同步过、价格未变因而没有 change 的模型同样要打标记，否则标记会永久缺失。
	//
	// 但只对「本条目的价格字段确实由同步写入了人民币值」的模型打标记。判据是
	// 历史同步记录里有 source=cny-official 的写入：若某条目因运营手配价被整条
	// 跳过，它的值仍是上一版美元价，此时打上人民币标记会让计费侧误以为这是人民币
	// 金额而跳过美元兜底，实际按美元数值计费 —— 值与标记错配。
	for model := range cnyModels {
		if _, written := cnyWrittenModels[model]; !written {
			continue
		}
		raw, exists := out[model]
		if !exists {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			continue
		}
		if fields["cny_official"] == true {
			continue
		}
		fields["cny_official"] = true
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
