package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
)

// 人民币官方价（cny-official）数据源。
//
// 背景：models.dev 只给美元参考价。国模厂商（阿里、智谱、月之暗面、MiniMax、
// 火山、阶跃……）的官方牌价是人民币原币价格，美元价是按汇率折算/取整的二手数字，
// 与官网公示的人民币价差得很远（实测 kimi-k2.6 官方 6.5 元 vs models.dev 报 0.95
// 美元，倍率 6.84；MiniMax-M2 官方 2.1 元 vs 0.3 美元，倍率 7.0）。要「按官方人民币
// 牌价收费」，只能另起一个数据源，把人民币数字照搬进覆盖层。
//
// 数据源：basellm/llm-metadata（Apache-2.0，每日更新）
//
//	https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json
//
// 结构（顶层 map[vendorID]provider）有两种人民币形态，都必须支持：
//
//  1. 独立的人民币分支：`cost.currency_options.CNY.{input,output,cache_read,...}`
//     （deepseek 用这种：美元在 cost.input，人民币在 currency_options.CNY）
//  2. 整个 provider 以人民币计价：`provider.currency == "CNY"` 或模型
//     `cost.currency == "CNY"`，此时 `cost.input` 本身就是人民币
//     （alibaba-cn / zhipuai / minimax-cn / volcengine … 用这种）
//
// 价格单位是「每百万 token」，覆盖层单位是「每 token」，统一除以 1e6。
//
// 三档口径优先级（brief 钉死）：usd < cny-official < manual
//   - usd：models.dev 同步写入的美元价（本文件不产生，只参与优先级比较）
//   - cny-official：本文件解析出的人民币官方价
//   - manual：运营手配。判定沿用 modelHasManualPricing（与上轮同步写入值不同），
//     另外 settings 里 pricing.cny_official.manual_overrides 命中的模型也算 manual。
//
// 国模硬要求：白名单厂商（或 provider.currency == "CNY"）的模型，数据源里没有
// 人民币价时**跳过不写**，绝不回退到美元价。回退会把这个模型按美元数字收费，
// 比「没配价」更糟。

const (
	// ModelPricingTierUSD 是美元参考价口径（models.dev）。
	ModelPricingTierUSD = "usd"
	// ModelPricingTierCNYOfficial 是人民币官方牌价口径（basellm/llm-metadata）。
	ModelPricingTierCNYOfficial = "cny-official"
	// ModelPricingTierManual 是运营手配口径，永不被同步覆盖。
	ModelPricingTierManual = "manual"

	// SettingKeyPricingCNYOfficial 是人民币官方价同步的 settings 键。
	SettingKeyPricingCNYOfficial = "pricing.cny_official"

	// cnyOfficialDefaultSourceURL 是人民币价数据源的默认地址。
	cnyOfficialDefaultSourceURL = "https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json"

	// cnyOfficialSiteTimezone 是站内分时配置使用的时区。数据源给的是 UTC，
	// 落盘前必须平移，否则整体错 8 小时。
	cnyOfficialSiteTimezone = "Asia/Shanghai"

	// cnyOfficialMultiplierTolerance 是长上下文阶梯 input/output 倍率的一致性容差。
	// 两侧倍率差异超过它就认为数据源的两档价来自不同价格版本，不能各自写：
	// 覆盖层只有一条阈值，取两者平均并在同步日志里留冲突记录。
	cnyOfficialMultiplierTolerance = 0.001

	// 覆盖层里表达长短上下文阶梯的三个字段（LiteLLMModelPricing 上都是标量）。
	cnyOfficialFieldThreshold  = "long_context_input_token_threshold"
	cnyOfficialFieldInputMult  = "long_context_input_cost_multiplier"
	cnyOfficialFieldOutputMult = "long_context_output_cost_multiplier"
)

// cnyOfficialProviderWhitelist 是实测的国模厂商（18 个，按 vendor id）。
//
// coding-plan / token-plan / step-plan 是套餐价（模型 cost 全 0，表示套餐内不再
// 按 token 计费），它们仍然必须进白名单：只有进白名单才能让「该厂商的模型一律
// 按人民币口径处理」，从而在套餐价不可用（全 0）时正确地跳过，而不是回退到
// models.dev 的美元价。
var cnyOfficialProviderWhitelist = map[string]struct{}{
	"alibaba-cn":             {},
	"alibaba-coding-plan-cn": {},
	"alibaba-token-plan-cn":  {},
	"bailing":                {},
	"minimax-cn":             {},
	"minimax-cn-coding-plan": {},
	"moonshotai-cn":          {},
	"sensenova":              {},
	"stepfun":                {},
	"stepfun-step-plan":      {},
	"tencent-coding-plan":    {},
	"tencent-token-plan":     {},
	"tencent-tokenhub":       {},
	"volcengine":             {},
	"volcengine-coding-plan": {},
	"xiaomi-token-plan-cn":   {},
	"zhipuai":                {},
	"zhipuai-coding-plan":    {},
}

// CNYOfficialPricingSettings 是 settings 键 pricing.cny_official 的内容。
type CNYOfficialPricingSettings struct {
	// Enabled 控制是否参与同步。键缺失时视为关闭：人民币数字写进覆盖层是一次
	// 定价口径切换，不能因为「配置还没写」就悄悄发生。
	Enabled bool `json:"enabled"`
	// SourceURL 允许运营换镜像源；留空用默认地址。
	SourceURL string `json:"source_url"`
	// ManualOverrides 命中的模型本轮同步跳过不写（尊重人工）。键支持
	// "<vendor>/<model>" 与裸 "<model>" 两种写法，统一小写比较。
	ManualOverrides map[string]CNYOfficialManualPrice `json:"manual_overrides"`
}

// CNYOfficialManualPrice 是 manual_overrides 里的一个价位（人民币 / 百万 token）。
// 本版同步只用它做「跳过」判定，不会把它写进覆盖层：人工价由运营自己在价卡
// 或覆盖层里配，同步只负责不覆盖。
type CNYOfficialManualPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// cnyOfficialSettingsReader 是本服务读取 settings 需要的最小接口。
// 生产环境传 *SettingService，测试传桩。
type cnyOfficialSettingsReader interface {
	GetRawSettings(ctx context.Context, keys []string) (map[string]string, error)
}

// parseCNYOfficialPricingSettings 解析 settings 原始值。
//
// 空值 / 键缺失 → (nil, nil)，表示本功能未启用。JSON 写坏 → 报错：此时既不能
// 当「关闭」处理（国模已经按人民币配了价，退回美元口径会把它们改回美元），
// 也不能猜默认值，只能让这轮同步失败并把错误暴露到 Status。
func parseCNYOfficialPricingSettings(raw string) (*CNYOfficialPricingSettings, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var settings CNYOfficialPricingSettings
	if err := json.Unmarshal([]byte(trimmed), &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SettingKeyPricingCNYOfficial, err)
	}
	if strings.TrimSpace(settings.SourceURL) == "" {
		settings.SourceURL = cnyOfficialDefaultSourceURL
	}
	return &settings, nil
}

// loadCNYOfficialPricingSettings 从 settings 读取配置。reader 为 nil 或键缺失
// 都返回 (nil, nil)，调用方据此按「只有 usd 一档」处理。
func loadCNYOfficialPricingSettings(ctx context.Context, reader cnyOfficialSettingsReader) (*CNYOfficialPricingSettings, error) {
	if reader == nil {
		return nil, nil
	}
	values, err := reader.GetRawSettings(ctx, []string{SettingKeyPricingCNYOfficial})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SettingKeyPricingCNYOfficial, err)
	}
	return parseCNYOfficialPricingSettings(values[SettingKeyPricingCNYOfficial])
}

// ---------------------------------------------------------------------------
// 数据源结构
// ---------------------------------------------------------------------------

// cnyOfficialRegistryProvider 是数据源里的一个厂商。
type cnyOfficialRegistryProvider struct {
	ID       string                              `json:"id"`
	Name     string                              `json:"name"`
	Currency string                              `json:"currency"`
	Models   map[string]cnyOfficialRegistryModel `json:"models"`
}

// cnyOfficialRegistryModel 是数据源里的单个模型。
type cnyOfficialRegistryModel struct {
	ID   string               `json:"id"`
	Cost *cnyOfficialCostBlob `json:"cost"`
}

// cnyOfficialCostBlob 是模型的 cost 块。单位「每百万 token」。
// 人民币可能在 base 字段（provider 以 CNY 计价），也可能在 currency_options.CNY。
type cnyOfficialCostBlob struct {
	Currency        string                               `json:"currency"`
	Input           *float64                             `json:"input"`
	Output          *float64                             `json:"output"`
	CacheRead       *float64                             `json:"cache_read"`
	CacheWrite      *float64                             `json:"cache_write"`
	Tiers           []cnyOfficialTier                    `json:"tiers"`
	Schedule        *cnyOfficialSchedule                 `json:"schedule"`
	CurrencyOptions map[string]cnyOfficialCurrencyOption `json:"currency_options"`
}

// cnyOfficialCurrencyOption 是 cost.currency_options.<CUR> 分支。
type cnyOfficialCurrencyOption struct {
	Currency   string               `json:"currency"`
	Input      *float64             `json:"input"`
	Output     *float64             `json:"output"`
	CacheRead  *float64             `json:"cache_read"`
	CacheWrite *float64             `json:"cache_write"`
	Tiers      []cnyOfficialTier    `json:"tiers"`
	Schedule   *cnyOfficialSchedule `json:"schedule"`
}

// cnyOfficialTier 是长短上下文阶梯的一档。tier.size 表示「超过该 token 数即进入
// 本档」，与站内 long_context_input_token_threshold 的严格大于语义一致。
type cnyOfficialTier struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
	Tier       struct {
		Size int    `json:"size"`
		Type string `json:"type"`
	} `json:"tier"`
}

// cnyOfficialSchedule 是分时谷峰。timezone 实测是 UTC，hours 是 UTC 时刻。
type cnyOfficialSchedule struct {
	Timezone string                      `json:"timezone"`
	Fallback string                      `json:"fallback"`
	Windows  []cnyOfficialScheduleWindow `json:"windows"`
}

// cnyOfficialScheduleWindow 是一个谷峰窗口。
type cnyOfficialScheduleWindow struct {
	Name      string   `json:"name"`
	Weekdays  []int    `json:"weekdays"`
	Hours     []string `json:"hours"`
	Input     *float64 `json:"input"`
	Output    *float64 `json:"output"`
	CacheRead *float64 `json:"cache_read"`
	Reasoning *float64 `json:"reasoning"`
}

// ---------------------------------------------------------------------------
// 解析结果
// ---------------------------------------------------------------------------

// cnyOfficialLongContext 是折算出的长上下文阶梯（覆盖层可表达的部分）。
type cnyOfficialLongContext struct {
	// Threshold 是第一档（最小 size）的 token 阈值。
	Threshold int
	// InputMultiplier / OutputMultiplier 是第一档价 ÷ 基础价。
	InputMultiplier  float64
	OutputMultiplier float64
	// TierCount 是数据源给出的总档数。>1 时更高档无法表达，只写第 1 档。
	TierCount int
}

// CNYOfficialPeakPrice 是一个谷峰窗口的绝对人民币价（每百万 token），
// 供价格核对脚本使用；站内分时配置只吃倍率。
type CNYOfficialPeakPrice struct {
	Name      string  `json:"name"`
	Weekdays  []int   `json:"weekdays"`
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
	CacheRead float64 `json:"cache_read"`
}

// CNYOfficialTimePricing 是解析并完成 UTC→Asia/Shanghai 平移后的分时价。
//
// 本版不落库到 channel_model_pricing.time_pricing（分组级配置由运营/后续脚本处理），
// 但解析结果必须可通过 Status() 查到：倍率是否一致、时段是否跨日都在这里。
type CNYOfficialTimePricing struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	// Timezone 恒为 Asia/Shanghai：Periods 里的时刻已经平移过。
	Timezone     string                     `json:"timezone"`
	WeekdaysOnly bool                       `json:"weekdays_only"`
	Fallback     string                     `json:"fallback"`
	Periods      []ChannelTimePricingPeriod `json:"periods"`
	// ChannelTimePricing 是可直接落库的形态（倍率已折算）。无法表达时为 nil，
	// 原因写在 Conflicts 里。
	ChannelTimePricing *ChannelTimePricing    `json:"channel_time_pricing,omitempty"`
	PeakPrices         []CNYOfficialPeakPrice `json:"peak_prices"`
	Conflicts          []string               `json:"conflicts,omitempty"`
}

// CNYOfficialSyncReport 是一轮解析的可观测结果。全部通过 Status() 暴露。
type CNYOfficialSyncReport struct {
	SourceURL      string `json:"source_url"`
	Providers      int    `json:"providers"`
	ProvidersCNY   int    `json:"providers_cny"`
	ModelEntries   int    `json:"model_entries"`
	UsablePrices   int    `json:"usable_prices"`
	NationalModels int    `json:"national_models"`
	// Applied / Skipped 在合并阶段填充。
	Applied int `json:"applied"`
	Skipped int `json:"skipped"`
	// Ladders 记录「N 档阶梯，本版只表达第 1 档」这类无法完整表达的项。
	Ladders []string `json:"ladders,omitempty"`
	// Conflicts 记录倍率冲突、时区无法表达等需要人工确认的项。
	Conflicts []string `json:"conflicts,omitempty"`
	// SkippedNoCNY 是国模里没有可用人民币价、被跳过且未回退美元价的模型。
	SkippedNoCNY []string `json:"skipped_no_cny,omitempty"`
	// TimePricing 是解析出的分时价（已平移时区）。
	TimePricing []CNYOfficialTimePricing `json:"time_pricing,omitempty"`
}

// cnyOfficialPricing 是某个模型最终采用的人民币价（每 token）。
type cnyOfficialPricing struct {
	Model       string
	Provider    string
	PerToken    map[string]float64
	LongContext *cnyOfficialLongContext
	TimePricing *CNYOfficialTimePricing
}

// cnyOfficialDataset 是一次抓取 + 解析的完整结果。
type cnyOfficialDataset struct {
	Pricing         map[string]*cnyOfficialPricing
	National        map[string]struct{}
	ManualOverrides map[string]struct{}
	Report          *CNYOfficialSyncReport
}

func (d *cnyOfficialDataset) pricing(model string) (*cnyOfficialPricing, bool) {
	if d == nil {
		return nil, false
	}
	priced, ok := d.Pricing[model]
	return priced, ok
}

// isNationalModel 报告该模型是否属于「国模」（出现在白名单厂商或 CNY 计价厂商下）。
func (d *cnyOfficialDataset) isNationalModel(model string) bool {
	if d == nil {
		return false
	}
	_, ok := d.National[model]
	return ok
}

// manualOverrideHit 报告 manual_overrides 是否命中该 (厂商, 模型)。
// 支持 "<vendor>/<model>" 与裸 "<model>" 两种写法。
func (d *cnyOfficialDataset) manualOverrideHit(model, provider string) bool {
	if d == nil || len(d.ManualOverrides) == 0 {
		return false
	}
	if _, ok := d.ManualOverrides[strings.ToLower(strings.TrimSpace(model))]; ok {
		return true
	}
	if provider != "" {
		key := strings.ToLower(strings.TrimSpace(provider)) + "/" + strings.ToLower(strings.TrimSpace(model))
		if _, ok := d.ManualOverrides[key]; ok {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 抓取与解析
// ---------------------------------------------------------------------------

// fetchCNYOfficialRegistry 抓取并解析人民币价数据源，复用现有 fetcher。
func (s *ModelPricingSyncService) fetchCNYOfficialRegistry(ctx context.Context, endpoint string) (map[string]cnyOfficialRegistryProvider, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = cnyOfficialDefaultSourceURL
	}
	body, err := s.fetcher.FetchPricingJSON(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch cny-official registry: %w", err)
	}
	if int64(len(body)) > modelPricingSyncBodyLimit {
		return nil, fmt.Errorf("cny-official registry exceeds %d bytes", modelPricingSyncBodyLimit)
	}
	var registry map[string]cnyOfficialRegistryProvider
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("parse cny-official registry: %w", err)
	}
	if len(registry) == 0 {
		return nil, fmt.Errorf("cny-official registry is empty")
	}
	// 顶层键是 vendor id，条目里可能没带 id，补齐后用 vendor id 做键。
	for vendorID, provider := range registry {
		if strings.TrimSpace(provider.ID) == "" {
			provider.ID = vendorID
			registry[vendorID] = provider
		}
	}
	return registry, nil
}

// loadCNYOfficialDataset 抓取 + 解析，产出一个可直接参与合并的数据集。
func (s *ModelPricingSyncService) loadCNYOfficialDataset(ctx context.Context, settings *CNYOfficialPricingSettings) (*cnyOfficialDataset, error) {
	sourceURL := strings.TrimSpace(settings.SourceURL)
	if sourceURL == "" {
		sourceURL = cnyOfficialDefaultSourceURL
	}
	registry, err := s.fetchCNYOfficialRegistry(ctx, sourceURL)
	if err != nil {
		return nil, err
	}
	dataset := buildCNYOfficialDataset(registry, settings, sourceURL)
	if len(dataset.Pricing) == 0 && len(dataset.National) == 0 {
		return nil, fmt.Errorf("cny-official registry contains no CNY-denominated model")
	}
	return dataset, nil
}

// isCNYOfficialProvider 判定厂商是否按人民币口径处理。
//
// 三条任一命中即为人民币厂商：
//  1. vendor id 在白名单（实测的国模清单）；
//  2. 数据源显式声明 provider.currency == "CNY"（alibaba-cn / zhipuai / minimax-cn …）；
//  3. 该厂商底下有模型带 cost.currency_options.CNY 独立分支。
//
// 第 3 条是必需的：deepseek 的 provider 没有 currency 字段、也不在白名单里，人民币
// 价只出现在模型的 currency_options.CNY（USD 价在 cost.input）。漏掉这条，
// deepseek-flash 就会退回 models.dev 的 0.15 USD 而不是官方 1 元人民币牌价。
func isCNYOfficialProvider(vendorID string, provider cnyOfficialRegistryProvider) bool {
	if _, ok := cnyOfficialProviderWhitelist[strings.ToLower(strings.TrimSpace(vendorID))]; ok {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(provider.Currency), "CNY") {
		return true
	}
	for _, model := range provider.Models {
		if model.Cost == nil {
			continue
		}
		if _, ok := cnyOfficialCurrencyOptionForCNY(model.Cost.CurrencyOptions); ok {
			return true
		}
	}
	return false
}

// isCNYOfficialPlanVendor 判定厂商是否为套餐（plan）条目。套餐条目的 cost 全 0，
// 不是按 token 的 API 价，排序上必须让位给真正的按量厂商。
func isCNYOfficialPlanVendor(vendorID string) bool {
	return strings.Contains(strings.ToLower(vendorID), "plan")
}

// cnyOfficialRawPrices 是某个模型在某个厂商下的人民币原币价（每百万 token）。
type cnyOfficialRawPrices struct {
	Provider   string
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Tiers      []cnyOfficialTier
	Schedule   *cnyOfficialSchedule
	// ScheduleFromCNYBranch 表示分时价取自人民币分支；false 表示回落到 base
	// schedule（谷峰窗口与倍率两侧一致，可复用）。
	ScheduleFromCNYBranch bool
}

func (p cnyOfficialRawPrices) hasAnyPrice() bool {
	return p.Input > 0 || p.Output > 0
}

func cnyOfficialDeref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// resolveCNYOfficialRawPrices 从模型 cost 块取人民币原币价。
// 优先 currency_options.CNY 独立分支，其次 provider/模型声明以 CNY 计价时的 base 字段。
func resolveCNYOfficialRawPrices(vendorID string, provider cnyOfficialRegistryProvider, model cnyOfficialRegistryModel) *cnyOfficialRawPrices {
	cost := model.Cost
	if cost == nil {
		return nil
	}
	if option, ok := cnyOfficialCurrencyOptionForCNY(cost.CurrencyOptions); ok {
		raw := &cnyOfficialRawPrices{
			Provider:              vendorID,
			Input:                 cnyOfficialDeref(option.Input),
			Output:                cnyOfficialDeref(option.Output),
			CacheRead:             cnyOfficialDeref(option.CacheRead),
			CacheWrite:            cnyOfficialDeref(option.CacheWrite),
			Tiers:                 option.Tiers,
			Schedule:              option.Schedule,
			ScheduleFromCNYBranch: true,
		}
		if raw.Schedule == nil {
			raw.Schedule = cost.Schedule
			raw.ScheduleFromCNYBranch = false
		}
		return raw
	}
	if strings.EqualFold(strings.TrimSpace(provider.Currency), "CNY") ||
		strings.EqualFold(strings.TrimSpace(cost.Currency), "CNY") {
		return &cnyOfficialRawPrices{
			Provider:   vendorID,
			Input:      cnyOfficialDeref(cost.Input),
			Output:     cnyOfficialDeref(cost.Output),
			CacheRead:  cnyOfficialDeref(cost.CacheRead),
			CacheWrite: cnyOfficialDeref(cost.CacheWrite),
			Tiers:      cost.Tiers,
			Schedule:   cost.Schedule,
		}
	}
	return nil
}

// cnyOfficialCurrencyOptionForCNY 大小写不敏感地取 CNY 分支。
func cnyOfficialCurrencyOptionForCNY(options map[string]cnyOfficialCurrencyOption) (cnyOfficialCurrencyOption, bool) {
	for currency, option := range options {
		if strings.EqualFold(strings.TrimSpace(currency), "CNY") {
			return option, true
		}
	}
	return cnyOfficialCurrencyOption{}, false
}

// buildCNYOfficialDataset 把数据源拍平成「模型 → 人民币价」。
//
// 同名模型可能出现在多个人民币厂商下（实测 27 例，如 qwen3-max 同时在
// alibaba-cn 与 alibaba-token-plan-cn，后者是套餐条目、cost 全 0）。
// 排序规则：非套餐厂商优先，其次按 vendor id 升序，保证结果确定性；
// 同一模型取第一个「有可用价格」的厂商，套餐的 0 价不会覆盖按量价。
func buildCNYOfficialDataset(registry map[string]cnyOfficialRegistryProvider, settings *CNYOfficialPricingSettings, sourceURL string) *cnyOfficialDataset {
	report := &CNYOfficialSyncReport{SourceURL: sourceURL, Providers: len(registry)}
	dataset := &cnyOfficialDataset{
		Pricing:         map[string]*cnyOfficialPricing{},
		National:        map[string]struct{}{},
		ManualOverrides: map[string]struct{}{},
		Report:          report,
	}

	vendors := make([]string, 0, len(registry))
	for vendorID := range registry {
		vendors = append(vendors, vendorID)
	}
	sort.Slice(vendors, func(i, j int) bool {
		planI, planJ := isCNYOfficialPlanVendor(vendors[i]), isCNYOfficialPlanVendor(vendors[j])
		if planI != planJ {
			return !planI
		}
		return vendors[i] < vendors[j]
	})

	for _, vendorID := range vendors {
		provider := registry[vendorID]
		if !isCNYOfficialProvider(vendorID, provider) {
			continue
		}
		report.ProvidersCNY++

		modelIDs := make([]string, 0, len(provider.Models))
		for modelID := range provider.Models {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)

		for _, modelID := range modelIDs {
			key := normalizeModelPricingSyncModelName(modelID)
			if key == "" {
				continue
			}
			// 只要厂商属于人民币口径，模型就进「国模」集合 —— 即使没有可用价。
			// 这一步是「国模无人民币价必须跳过、绝不回退美元」的判据来源。
			dataset.National[key] = struct{}{}
			report.ModelEntries++

			raw := resolveCNYOfficialRawPrices(vendorID, provider, provider.Models[modelID])
			if raw == nil || !raw.hasAnyPrice() {
				continue
			}
			if _, exists := dataset.Pricing[key]; exists {
				// 已有更优厂商的按量价，保留先出现的那个。
				continue
			}
			priced := &cnyOfficialPricing{
				Model:    key,
				Provider: vendorID,
				PerToken: map[string]float64{
					"input_cost_per_token":            raw.Input / 1e6,
					"output_cost_per_token":           raw.Output / 1e6,
					"cache_read_input_token_cost":     raw.CacheRead / 1e6,
					"cache_creation_input_token_cost": raw.CacheWrite / 1e6,
				},
				LongContext: buildCNYOfficialLongContext(key, *raw, report),
				TimePricing: buildCNYOfficialTimePricing(key, *raw, report),
			}
			dataset.Pricing[key] = priced
			report.UsablePrices++
		}
	}

	report.NationalModels = len(dataset.National)
	for model := range dataset.National {
		if _, ok := dataset.Pricing[model]; ok {
			continue
		}
		report.SkippedNoCNY = append(report.SkippedNoCNY, model)
	}
	sort.Strings(report.SkippedNoCNY)

	if settings != nil {
		for key := range settings.ManualOverrides {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "" {
				continue
			}
			dataset.ManualOverrides[normalized] = struct{}{}
		}
	}
	sort.Strings(report.Ladders)
	sort.Strings(report.Conflicts)
	return dataset
}

// buildCNYOfficialLongContext 把阶梯折算成覆盖层能表达的单阈值 + 倍率。
//
// 覆盖层只有一个阈值和两侧倍率，数据源可以有任意多档（qwen3-max 实测两档：
// 32000 / 128000）。本版只表达第一档（最小 size）：更高档写不进去，落日志。
//
// 倍率按 input / output 各自算。两者差异超过容差时说明数据源两档价来自不同
// 价格版本（或某侧取整粗糙），不能各写各的 —— 覆盖层里阈值只有一条，
// 取两者平均并把冲突写进同步日志，不阻断同步。
func buildCNYOfficialLongContext(model string, raw cnyOfficialRawPrices, report *CNYOfficialSyncReport) *cnyOfficialLongContext {
	tiers := make([]cnyOfficialTier, 0, len(raw.Tiers))
	for _, tier := range raw.Tiers {
		if tier.Tier.Size <= 0 {
			continue
		}
		tiers = append(tiers, tier)
	}
	if len(tiers) == 0 {
		return nil
	}
	sort.SliceStable(tiers, func(i, j int) bool { return tiers[i].Tier.Size < tiers[j].Tier.Size })

	first := tiers[0]
	inputMultiplier, outputMultiplier := 1.0, 1.0
	if first.Input != nil && raw.Input > 0 {
		inputMultiplier = *first.Input / raw.Input
	}
	if first.Output != nil && raw.Output > 0 {
		outputMultiplier = *first.Output / raw.Output
	}

	if inputMultiplier <= 1 && outputMultiplier <= 1 {
		// 阶梯价不高于基础价：覆盖层的倍率语义是「超过阈值后加价」，
		// 表达不了「越长越便宜」，按站内既有约定不生成阶梯。
		report.Conflicts = append(report.Conflicts, fmt.Sprintf(
			"%s: 第一档阶梯价不高于基础价（input x%.4f, output x%.4f），本版不生成长上下文阶梯",
			model, inputMultiplier, outputMultiplier))
		return nil
	}
	if !cnyOfficialFinitePositive(inputMultiplier) || !cnyOfficialFinitePositive(outputMultiplier) {
		report.Conflicts = append(report.Conflicts, fmt.Sprintf(
			"%s: 阶梯倍率非有限正数（input x%v, output x%v），跳过阶梯", model, inputMultiplier, outputMultiplier))
		return nil
	}

	if math.Abs(inputMultiplier-outputMultiplier) > cnyOfficialMultiplierTolerance {
		averaged := (inputMultiplier + outputMultiplier) / 2
		report.Conflicts = append(report.Conflicts, fmt.Sprintf(
			"%s: 长上下文倍率两侧不一致（input x%.6f, output x%.6f，差 %.6f > %.3f），已取平均 x%.6f 写入",
			model, inputMultiplier, outputMultiplier,
			math.Abs(inputMultiplier-outputMultiplier), cnyOfficialMultiplierTolerance, averaged))
		inputMultiplier, outputMultiplier = averaged, averaged
	}

	ladder := &cnyOfficialLongContext{
		Threshold:        first.Tier.Size,
		InputMultiplier:  inputMultiplier,
		OutputMultiplier: outputMultiplier,
		TierCount:        len(tiers),
	}
	if len(tiers) > 1 {
		sizes := make([]string, 0, len(tiers))
		for _, tier := range tiers {
			sizes = append(sizes, fmt.Sprintf("%d", tier.Tier.Size))
		}
		report.Ladders = append(report.Ladders, fmt.Sprintf(
			"%s: %d 档阶梯（%s），本版只表达第 1 档（%d）",
			model, len(tiers), strings.Join(sizes, "/"), first.Tier.Size))
	}

	// 缓存读的阶梯档在站内统一跟随输入倍率，不单独折算。倍率明显不同时记一条，
	// 避免「看起来写了但其实少收/多收」的沉默偏差。
	if first.CacheRead != nil && raw.CacheRead > 0 {
		cacheRatio := *first.CacheRead / raw.CacheRead
		if math.Abs(cacheRatio-inputMultiplier) > cnyOfficialMultiplierTolerance {
			report.Conflicts = append(report.Conflicts, fmt.Sprintf(
				"%s: 阶梯档 cache_read 倍率 x%.6f 与输入倍率 x%.6f 不一致，站内按输入倍率计费",
				model, cacheRatio, inputMultiplier))
		}
	}
	return ladder
}

func cnyOfficialFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

// buildCNYOfficialTimePricing 解析分时谷峰并把 UTC 时刻平移成 Asia/Shanghai。
//
// 站内 ChannelTimePricing 只表达「倍率 + 星期开关」，没有逐窗口绝对价、
// 也没有任意星期集合。所以这里：
//   - 时段：hours 是 UTC，+8 小时得站内时刻，跨日则星期顺延；
//   - 倍率：窗口价 ÷ 基础价（fallback=off_peak，基础价即谷价）；
//   - 可表达时才给 ChannelTimePricing（星期集合恰为工作日 → WeekdaysOnly），
//     否则记冲突。倍率不一致同样记冲突（落库取输入倍率，其余靠日志核对）。
func buildCNYOfficialTimePricing(model string, raw cnyOfficialRawPrices, report *CNYOfficialSyncReport) *CNYOfficialTimePricing {
	schedule := raw.Schedule
	if schedule == nil || len(schedule.Windows) == 0 {
		return nil
	}
	if !raw.ScheduleFromCNYBranch {
		report.Conflicts = append(report.Conflicts, fmt.Sprintf(
			"%s: 分时价取自非人民币分支的 schedule（谷峰窗口两侧一致，倍率按人民币价折算）", model))
	}

	result := &CNYOfficialTimePricing{
		Model:    model,
		Provider: raw.Provider,
		Timezone: cnyOfficialSiteTimezone,
		Fallback: strings.TrimSpace(schedule.Fallback),
	}

	weekdaySets := make([][]int, 0, len(schedule.Windows))
	allWeekdaysOnly := true
	for _, window := range schedule.Windows {
		result.PeakPrices = append(result.PeakPrices, CNYOfficialPeakPrice{
			Name:      window.Name,
			Weekdays:  append([]int(nil), window.Weekdays...),
			Input:     cnyOfficialDeref(window.Input),
			Output:    cnyOfficialDeref(window.Output),
			CacheRead: cnyOfficialDeref(window.CacheRead),
		})

		multiplier := cnyOfficialWindowMultiplier(raw, window, model, report)
		if multiplier <= 0 {
			continue
		}

		for _, hourRange := range window.Hours {
			start, end, err := cnyOfficialShiftHoursToSiteTimezone(hourRange, schedule.Timezone)
			if err != nil {
				report.Conflicts = append(report.Conflicts, fmt.Sprintf("%s: 时窗 %q 无法平移：%v", model, hourRange, err))
				continue
			}
			shiftedWeekdays := cnyOfficialShiftWeekdays(window.Weekdays, hourRange, schedule.Timezone)
			weekdaySets = append(weekdaySets, shiftedWeekdays)
			if !cnyOfficialIsWorkdaySet(shiftedWeekdays) {
				allWeekdaysOnly = false
			}
			result.Periods = append(result.Periods, ChannelTimePricingPeriod{
				StartTime:  start,
				EndTime:    end,
				Multiplier: multiplier,
			})
		}
	}

	if len(result.Periods) == 0 {
		return nil
	}
	sort.Slice(result.Periods, func(i, j int) bool {
		return result.Periods[i].StartTime < result.Periods[j].StartTime
	})

	if allWeekdaysOnly {
		result.WeekdaysOnly = true
		config := &ChannelTimePricing{
			Timezone:     cnyOfficialSiteTimezone,
			WeekdaysOnly: true,
			Periods:      append([]ChannelTimePricingPeriod(nil), result.Periods...),
		}
		// 用站内校验器兜底：倍率精度、时段重叠、跨日边界只要不合规就不给落库形态。
		if err := validateChannelTimePricing(config); err != nil {
			result.Conflicts = append(result.Conflicts, fmt.Sprintf("%s: 分时配置未通过站内校验：%v", model, err))
		} else {
			result.ChannelTimePricing = config
		}
	} else {
		result.Conflicts = append(result.Conflicts, fmt.Sprintf(
			"%s: 谷峰窗口的星期集合不是「周一至周五」，站内分时配置只能表达工作日/全周开关", model))
	}

	report.TimePricing = append(report.TimePricing, *result)
	return result
}

// cnyOfficialWindowMultiplier 折算窗口倍率 = 窗口价 ÷ 基础价。
// input / output / cache_read 三者倍率不一致时记冲突，落库取输入倍率
// （站内分时配置只有一个 multiplier 字段）。
func cnyOfficialWindowMultiplier(raw cnyOfficialRawPrices, window cnyOfficialScheduleWindow, model string, report *CNYOfficialSyncReport) float64 {
	ratios := make(map[string]float64, 3)
	if window.Input != nil && raw.Input > 0 {
		ratios["input"] = *window.Input / raw.Input
	}
	if window.Output != nil && raw.Output > 0 {
		ratios["output"] = *window.Output / raw.Output
	}
	if window.CacheRead != nil && raw.CacheRead > 0 {
		ratios["cache_read"] = *window.CacheRead / raw.CacheRead
	}
	if len(ratios) == 0 {
		return 0
	}
	names := make([]string, 0, len(ratios))
	for name := range ratios {
		names = append(names, name)
	}
	sort.Strings(names)
	base := ratios["input"]
	if base <= 0 {
		base = ratios[names[0]]
	}
	if !cnyOfficialFinitePositive(base) {
		return 0
	}
	for _, name := range names {
		if math.Abs(ratios[name]-base) > cnyOfficialMultiplierTolerance {
			report.Conflicts = append(report.Conflicts, fmt.Sprintf(
				"%s: 窗口 %q 的 %s 倍率 x%.6f 与基准 x%.6f 不一致，分时落库取基准倍率",
				model, window.Name, name, ratios[name], base))
		}
	}
	// 站内要求倍率最多两位小数且 ≥ 0.01。
	rounded := math.Round(base*100) / 100
	if rounded <= 0 {
		return 0
	}
	if math.Abs(rounded-base) > 1e-9 {
		report.Conflicts = append(report.Conflicts, fmt.Sprintf(
			"%s: 窗口 %q 倍率 x%.6f 超出两位小数精度，已取 x%.2f",
			model, window.Name, base, rounded))
	}
	return rounded
}

// cnyOfficialShiftHoursToSiteTimezone 把 "HH:mm-HH:mm" 从数据源时区平移到 Asia/Shanghai。
// 只支持 UTC / Etc/UTC / GMT 与已经是站内时区这两种输入，其余视为无法平移。
func cnyOfficialShiftHoursToSiteTimezone(hourRange, sourceTimezone string) (string, string, error) {
	bounds := strings.SplitN(strings.TrimSpace(hourRange), "-", 2)
	if len(bounds) != 2 {
		return "", "", fmt.Errorf("时窗必须形如 HH:mm-HH:mm")
	}
	startSecond, err := cnyOfficialParseClock(bounds[0])
	if err != nil {
		return "", "", err
	}
	endSecond, err := cnyOfficialParseClock(bounds[1])
	if err != nil {
		return "", "", err
	}
	offset, err := cnyOfficialTimezoneOffsetSeconds(sourceTimezone)
	if err != nil {
		return "", "", err
	}
	if offset == 0 {
		return cnyOfficialFormatClock(startSecond), cnyOfficialFormatClock(endSecond), nil
	}
	startSecond = cnyOfficialWrapSecond(startSecond + offset)
	endSecond = cnyOfficialWrapSecond(endSecond + offset)
	return cnyOfficialFormatClock(startSecond), cnyOfficialFormatClock(endSecond), nil
}

// cnyOfficialTimezoneOffsetSeconds 返回数据源时区相对 Asia/Shanghai 需要平移的秒数。
// 站内只接受 UTC 与站内时区两种来源：谷峰配置写死 UTC + 站内都是 UTC+8。
func cnyOfficialTimezoneOffsetSeconds(sourceTimezone string) (int, error) {
	switch strings.ToUpper(strings.TrimSpace(sourceTimezone)) {
	case "", "UTC", "ETC/UTC", "GMT", "Z":
		return 8 * 3600, nil
	case "ASIA/SHANGHAI", "CST", "PRC", "UTC+8", "+08:00":
		return 0, nil
	default:
		return 0, fmt.Errorf("不支持的源时区 %q（只支持 UTC 与 Asia/Shanghai）", sourceTimezone)
	}
}

func cnyOfficialParseClock(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	parts := strings.Split(trimmed, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("时刻 %q 必须形如 HH:mm", value)
	}
	var hour, minute int
	if _, err := fmt.Sscanf(parts[0], "%d", &hour); err != nil {
		return 0, fmt.Errorf("时刻 %q 的小时不合法", value)
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &minute); err != nil {
		return 0, fmt.Errorf("时刻 %q 的分钟不合法", value)
	}
	if hour < 0 || hour > 24 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("时刻 %q 超出范围", value)
	}
	return hour*3600 + minute*60, nil
}

// cnyOfficialWrapSecond 把秒数规整进 [0, 86400)。
func cnyOfficialWrapSecond(second int) int {
	const day = 24 * 3600
	second %= day
	if second < 0 {
		second += day
	}
	return second
}

// cnyOfficialFormatClock 输出站内接受的 HH:mm。回绕到 0 点时写 "00:00"：
// 站内解析器把结束侧的 "00:00" 当作 "24:00"（见 parseChannelTime），起始侧的
// "00:00" 当作 0 点，两种语义都正确。
func cnyOfficialFormatClock(second int) string {
	second = cnyOfficialWrapSecond(second)
	return fmt.Sprintf("%02d:%02d", second/3600, (second%3600)/60)
}

// cnyOfficialShiftWeekdays 平移时区后顺延星期（跨日窗口的星期要跟着变）。
// 星期沿用数据源的 1=周一 … 7=周日。
func cnyOfficialShiftWeekdays(weekdays []int, hourRange, sourceTimezone string) []int {
	if len(weekdays) == 0 {
		return nil
	}
	offset, err := cnyOfficialTimezoneOffsetSeconds(sourceTimezone)
	if err != nil || offset == 0 {
		return append([]int(nil), weekdays...)
	}
	bounds := strings.SplitN(strings.TrimSpace(hourRange), "-", 2)
	dayShift := 0
	if len(bounds) == 2 {
		if startSecond, err := cnyOfficialParseClock(bounds[0]); err == nil {
			if startSecond+offset >= 24*3600 {
				dayShift = 1
			}
		}
	}
	if dayShift == 0 {
		return append([]int(nil), weekdays...)
	}
	shifted := make([]int, 0, len(weekdays))
	for _, weekday := range weekdays {
		next := weekday + dayShift
		if next > 7 {
			next -= 7
		}
		if next < 1 {
			next += 7
		}
		shifted = append(shifted, next)
	}
	return shifted
}

// cnyOfficialIsWorkdaySet 判定星期集合是否恰为周一至周五（站内 WeekdaysOnly 的唯一形态）。
func cnyOfficialIsWorkdaySet(weekdays []int) bool {
	if len(weekdays) != 5 {
		return false
	}
	seen := make(map[int]struct{}, len(weekdays))
	for _, weekday := range weekdays {
		seen[weekday] = struct{}{}
	}
	for _, weekday := range []int{1, 2, 3, 4, 5} {
		if _, ok := seen[weekday]; !ok {
			return false
		}
	}
	return true
}

// cnyOfficialReportOf 安全取解析报告：dataset 为 nil（人民币档关闭）时返回 nil。
// Status 记录路径据此区分「这份报告不存在」与「报告为空」。
func cnyOfficialReportOf(dataset *cnyOfficialDataset) *CNYOfficialSyncReport {
	if dataset == nil {
		return nil
	}
	return dataset.Report
}

// logCNYOfficialReport 把无法表达 / 需要人工确认的项打进同步日志，保证可查。
func logCNYOfficialReport(report *CNYOfficialSyncReport) {
	if report == nil {
		return
	}
	slog.Info("cny-official pricing parsed",
		"source", report.SourceURL,
		"providers", report.Providers,
		"cny_providers", report.ProvidersCNY,
		"model_entries", report.ModelEntries,
		"usable_prices", report.UsablePrices,
		"national_models", report.NationalModels,
		"ladders", len(report.Ladders),
		"conflicts", len(report.Conflicts),
		"skipped_no_cny", len(report.SkippedNoCNY),
		"time_pricing", len(report.TimePricing),
	)
	for _, ladder := range report.Ladders {
		slog.Info("cny-official long-context ladder", "detail", ladder)
	}
	for _, conflict := range report.Conflicts {
		slog.Warn("cny-official pricing conflict", "detail", conflict)
	}
	for _, model := range report.SkippedNoCNY {
		slog.Warn("cny-official model skipped: no CNY price, USD fallback suppressed", "model", model)
	}
}
