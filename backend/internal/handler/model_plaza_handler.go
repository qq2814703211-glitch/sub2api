package handler

import (
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ModelPlazaHandler 处理「模型广场」查询。
//
// 广场路由挂 OptionalJWT 中间件：匿名可访问（除非 require_auth 开启），带 token 则
// 识别用户。可见性规则（橱窗语义，与「可用渠道」的可绑定语义不同）：
//   - 匿名：仅非专属分组（订阅型照常展示）；
//   - 登录：非专属分组 + user_allowed_groups 授权或持有有效订阅的专属分组；
//     若该用户开启了公开分组限制，则公开分组同样需要落在授权集合内。
type ModelPlazaHandler struct {
	plazaService   *service.ModelPlazaService
	apiKeyService  *service.APIKeyService
	settingService *service.SettingService
}

// NewModelPlazaHandler 创建模型广场 handler。
func NewModelPlazaHandler(
	plazaService *service.ModelPlazaService,
	apiKeyService *service.APIKeyService,
	settingService *service.SettingService,
) *ModelPlazaHandler {
	return &ModelPlazaHandler{
		plazaService:   plazaService,
		apiKeyService:  apiKeyService,
		settingService: settingService,
	}
}

// modelPlazaOfficialPricing 全局目录参考价，按计费目录口径返回。
type modelPlazaOfficialPricing struct {
	BillingMode        service.BillingMode `json:"billing_mode,omitempty"`
	UnitPrice          *float64            `json:"unit_price,omitempty"`
	InputPrice         *float64            `json:"input_price"`
	OutputPrice        *float64            `json:"output_price"`
	CacheWritePrice    *float64            `json:"cache_write_price"`
	CacheWrite1hPrice  *float64            `json:"cache_write_1h_price,omitempty"`
	CacheWrite30mPrice *float64            `json:"cache_write_30m_price,omitempty"`
	CacheWrite12hPrice *float64            `json:"cache_write_12h_price,omitempty"`
	CacheReadPrice     *float64            `json:"cache_read_price"`
	// Intervals 官方长上下文阶梯，仅多档模型给出。
	Intervals []modelPlazaOfficialPricingInterval `json:"intervals,omitempty"`
	// PriceWindows carries absolute rates per token, without group/user multipliers.
	PriceWindows []service.PriceSyncWindow `json:"price_windows,omitempty"`
}

type modelPlazaOfficialPricingInterval struct {
	userPricingIntervalDTO
	CacheWrite30mPrice *float64 `json:"cache_write_30m_price,omitempty"`
}

// modelPlazaTimePricingPeriod 分时倍率时段（配置时区当天 [start, end)）。
type modelPlazaTimePricingPeriod struct {
	StartTime  string  `json:"start_time"`
	EndTime    string  `json:"end_time"`
	Multiplier float64 `json:"multiplier"`
}

// modelPlazaTimePricing 计费会生效的分时倍率（仅倍率 ≠ 1 的时段）。
// WeekdaysOnly 为 true 时时段仅周一至周五生效，周末整天按标准价计费。
type modelPlazaTimePricing struct {
	Timezone     string                        `json:"timezone"`
	WeekdaysOnly bool                          `json:"weekdays_only,omitempty"`
	Periods      []modelPlazaTimePricingPeriod `json:"periods"`
}

// modelPlazaMediaRateOption discloses the rate selected by the actual request endpoint.
// A pricing card alone does not identify the endpoint, including request/token rules.
type modelPlazaMediaRateOption struct {
	RequestType string  `json:"request_type"`
	Multiplier  float64 `json:"multiplier"`
}

// modelPlazaPricing 广场实付定价：用户可见白名单字段 + 广场专属的 30m 缓存写价。
// 30m 只来自 service 层按分组/用户实际生效定价做的只读投影；缺项时省略字段，
// 绝不用 official_pricing 的目录参考价顶替售价。
type modelPlazaPricing struct {
	userSupportedModelPricing
	CacheWrite30mPrice *float64                    `json:"cache_write_30m_price,omitempty"`
	Intervals          []modelPlazaPricingInterval `json:"intervals"`
}

// modelPlazaPricingInterval 实付阶梯：白名单区间字段 + 该档 30m 售价（可为显式 0）。
type modelPlazaPricingInterval struct {
	userPricingIntervalDTO
	CacheWrite30mPrice *float64 `json:"cache_write_30m_price,omitempty"`
}

// modelPlazaModel 广场模型条目：实收口径展示定价（白名单形态）+ 官方参考价。
type modelPlazaModel struct {
	Name            string                     `json:"name"`
	Platform        string                     `json:"platform"`
	Pricing         *modelPlazaPricing         `json:"pricing"`
	OfficialPricing *modelPlazaOfficialPricing `json:"official_pricing"`
	// LongContextBasis 多档时的计价基准："whole_request"（整单按档）| "marginal"（仅超出部分）。
	LongContextBasis string `json:"long_context_basis,omitempty"`
	// TimePricing 分时倍率时段，落在时段内的请求整单乘倍率；无分时省略。
	TimePricing      *modelPlazaTimePricing      `json:"time_pricing,omitempty"`
	PriceWindows     []service.PriceSyncWindow   `json:"price_windows,omitempty"`
	MediaRateOptions []modelPlazaMediaRateOption `json:"media_rate_options,omitempty"`
}

// modelPlazaGroup 广场分组条目（白名单字段）。
type modelPlazaGroup struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Platform           string   `json:"platform"`
	SubscriptionType   string   `json:"subscription_type"`
	RateMultiplier     float64  `json:"rate_multiplier"`
	UserRateMultiplier *float64 `json:"user_rate_multiplier,omitempty"`
	PeakRateEnabled    bool     `json:"peak_rate_enabled"`
	PeakStart          string   `json:"peak_start"`
	PeakEnd            string   `json:"peak_end"`
	PeakRateMultiplier float64  `json:"peak_rate_multiplier"`
	IsExclusive        bool     `json:"is_exclusive"`
	// 生图独立倍率：为 true 时图片计费模型的实付倍率取 ImageRateMultiplier，
	// 不取分组/用户专属倍率。
	ImageRateIndependent bool    `json:"image_rate_independent"`
	ImageRateMultiplier  float64 `json:"image_rate_multiplier"`
	VideoRateIndependent bool    `json:"video_rate_independent"`
	VideoRateMultiplier  float64 `json:"video_rate_multiplier"`
	// 分组是否启用长上下文阶梯计费；关闭时模型实付列只展示最低档/基础价。
	LongContextPricingEnabled bool              `json:"long_context_pricing_enabled"`
	Models                    []modelPlazaModel `json:"models"`
}

// modelPlazaResponse 广场页响应。
type modelPlazaResponse struct {
	Description string            `json:"description"`
	Groups      []modelPlazaGroup `json:"groups"`
}

// Get 返回模型广场数据。
// GET /api/v1/model-plaza
func (h *ModelPlazaHandler) Get(c *gin.Context) {
	if h.settingService == nil {
		response.NotFound(c, "Model plaza is not enabled")
		return
	}
	rt := h.settingService.GetModelPlazaRuntime(c.Request.Context())
	if !rt.Enabled {
		response.NotFound(c, "Model plaza is not enabled")
		return
	}

	subject, authed := middleware.GetAuthSubjectFromContext(c)
	if rt.RequireAuth && !authed {
		response.Unauthorized(c, "Authentication required")
		return
	}

	groups, err := h.plazaService.ListGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// allowedGroups == nil 表示匿名；登录用户恒为非 nil（可能为空集合）。
	var allowedGroups map[int64]struct{}
	var restrictPublicGroups bool
	var userRates map[int64]float64
	if authed {
		allowedGroups, restrictPublicGroups, err = h.apiKeyService.GetUserGroupVisibility(c.Request.Context(), subject.UserID)
		if err != nil {
			// 可见性数据拿不到时不能静默降级成匿名视图（会错漏专属分组），直接报错。
			response.ErrorFrom(c, err)
			return
		}
		userRates, err = h.apiKeyService.GetUserGroupRates(c.Request.Context(), subject.UserID)
		if err != nil {
			// 专属倍率仅是展示增强，失败降级为分组默认倍率。
			slog.Warn("model_plaza_user_rates_failed", "error", err, "user_id", subject.UserID)
			userRates = nil
		}
	}

	visible := filterPlazaVisibleGroups(groups, allowedGroups, restrictPublicGroups)

	// 广场可匿名访问，分组文案随请求语言变化，提示中间缓存按语言区分。
	// 追加而非覆盖：CORS 中间件已为具体来源写入 Vary: Origin。
	c.Writer.Header().Add("Vary", "Accept-Language")
	out := toLocalizedModelPlazaGroups(visible, userRates, c.GetHeader("Accept-Language"))
	response.Success(c, modelPlazaResponse{
		Description: rt.Description,
		Groups:      out,
	})
}

// filterPlazaVisibleGroups 按登录态裁剪分组可见性。
// allowedGroups == nil 表示匿名（仅非专属）；非 nil 包含普通授权及有效订阅分组。
// restrictPublicGroups 为 true 时，公开分组也必须落在 allowedGroups 内，否则用户会
// 在广场看到自己实际绑定不了的分组。
func filterPlazaVisibleGroups(
	groups []service.PlazaGroup,
	allowedGroups map[int64]struct{},
	restrictPublicGroups bool,
) []service.PlazaGroup {
	visible := make([]service.PlazaGroup, 0, len(groups))
	for _, g := range groups {
		if g.IsExclusive || (restrictPublicGroups && allowedGroups != nil) {
			if allowedGroups == nil {
				continue
			}
			if _, ok := allowedGroups[g.ID]; !ok {
				continue
			}
		}
		visible = append(visible, g)
	}
	return visible
}

// toLocalizedModelPlazaGroups 转换可见分组，并把名称与描述换成请求首选语言的版本。
func toLocalizedModelPlazaGroups(visible []service.PlazaGroup, userRates map[int64]float64, acceptLanguage string) []modelPlazaGroup {
	out := make([]modelPlazaGroup, 0, len(visible))
	for i := range visible {
		group := toModelPlazaGroupDTO(&visible[i], userRates)
		group.Name, group.Description = visible[i].I18n.Localize(acceptLanguage, group.Name, group.Description)
		out = append(out, group)
	}
	return out
}

// toModelPlazaGroupDTO 将 service 层广场分组映射为白名单 DTO,并合并用户专属倍率。
func toModelPlazaGroupDTO(g *service.PlazaGroup, userRates map[int64]float64) modelPlazaGroup {
	models := make([]modelPlazaModel, 0, len(g.Models))
	for i := range g.Models {
		m := &g.Models[i]
		models = append(models, modelPlazaModel{
			Name:             m.Name,
			Platform:         m.Platform,
			Pricing:          toModelPlazaPricing(m.Pricing, m.CacheWrite30m),
			OfficialPricing:  toModelPlazaOfficialPricing(m.OfficialPricing),
			LongContextBasis: string(m.LongContextBasis),
			TimePricing:      toModelPlazaTimePricing(m.TimePricing),
			PriceWindows:     m.PriceWindows,
			MediaRateOptions: toModelPlazaMediaRateOptions(m.Pricing, g, userRates),
		})
	}
	dto := modelPlazaGroup{
		ID:                        g.ID,
		Name:                      g.Name,
		Description:               g.Description,
		Platform:                  g.Platform,
		SubscriptionType:          g.SubscriptionType,
		RateMultiplier:            g.RateMultiplier,
		PeakRateEnabled:           g.PeakRateEnabled,
		PeakStart:                 g.PeakStart,
		PeakEnd:                   g.PeakEnd,
		PeakRateMultiplier:        g.PeakRateMultiplier,
		IsExclusive:               g.IsExclusive,
		ImageRateIndependent:      g.ImageRateIndependent,
		ImageRateMultiplier:       g.ImageRateMultiplier,
		VideoRateIndependent:      g.VideoRateIndependent,
		VideoRateMultiplier:       g.VideoRateMultiplier,
		LongContextPricingEnabled: g.LongContextPricingEnabled,
		Models:                    models,
	}
	if rate, ok := userRates[g.ID]; ok {
		dto.UserRateMultiplier = &rate
	}
	return dto
}

// Keep these options endpoint-scoped: billing_mode and rule units do not identify
// whether a non-token media card is called from an image or video endpoint.
func toModelPlazaMediaRateOptions(pricing *service.ChannelModelPricing, g *service.PlazaGroup, userRates map[int64]float64) []modelPlazaMediaRateOption {
	if pricing == nil || (pricing.MediaPricing == nil && pricing.CatalogUnitPrice == nil) {
		return nil
	}
	rate := g.RateMultiplier
	if userRate, ok := userRates[g.ID]; ok {
		rate = userRate
	}
	if pricing.BillingMode == "" || pricing.BillingMode == service.BillingModeToken {
		return []modelPlazaMediaRateOption{{RequestType: "all", Multiplier: rate}}
	}
	imageRate, videoRate := rate, rate
	if g.ImageRateIndependent {
		imageRate = max(0, g.ImageRateMultiplier)
	}
	if g.VideoRateIndependent {
		videoRate = max(0, g.VideoRateMultiplier)
	}
	if imageRate == rate && videoRate == rate {
		return []modelPlazaMediaRateOption{{RequestType: "all", Multiplier: rate}}
	}
	return []modelPlazaMediaRateOption{
		{RequestType: "general", Multiplier: rate},
		{RequestType: "image", Multiplier: imageRate},
		{RequestType: "video", Multiplier: videoRate},
	}
}

// toModelPlazaTimePricing 转换分时倍率；nil 透传（JSON 省略）。
func toModelPlazaTimePricing(p *service.TimePricingSchedule) *modelPlazaTimePricing {
	if p == nil || len(p.Periods) == 0 {
		return nil
	}
	periods := make([]modelPlazaTimePricingPeriod, 0, len(p.Periods))
	for _, period := range p.Periods {
		periods = append(periods, modelPlazaTimePricingPeriod{
			StartTime:  period.StartTime,
			EndTime:    period.EndTime,
			Multiplier: period.Multiplier,
		})
	}
	return &modelPlazaTimePricing{Timezone: p.Timezone, WeekdaysOnly: p.WeekdaysOnly, Periods: periods}
}

// toModelPlazaPricing 组装广场实付定价：先取用户可见白名单定价，再叠加 service
// 投影的 30m 售价（基础档与逐阶梯档）。w 为 nil 或缺该档时字段省略，不用参考价补。
func toModelPlazaPricing(p *service.ChannelModelPricing, w *service.PlazaCacheWrite30m) *modelPlazaPricing {
	base := toUserPricing(p)
	if base == nil {
		return nil
	}
	out := &modelPlazaPricing{
		userSupportedModelPricing: *base,
		Intervals:                 make([]modelPlazaPricingInterval, len(base.Intervals)),
	}
	for i := range base.Intervals {
		out.Intervals[i] = modelPlazaPricingInterval{userPricingIntervalDTO: base.Intervals[i]}
	}
	if w == nil {
		return out
	}
	out.CacheWrite30mPrice = w.Base
	// 逐档只在档数与实付阶梯完全一致时映射，避免错位把某档 30m 挂到另一档上。
	if len(w.Intervals) != len(out.Intervals) {
		return out
	}
	for i := range out.Intervals {
		out.Intervals[i].CacheWrite30mPrice = w.Intervals[i]
	}
	return out
}

// toModelPlazaOfficialPricing 转换官方参考价；nil 透传（前端显示 "-"）。
func toModelPlazaOfficialPricing(p *service.PlazaOfficialPricing) *modelPlazaOfficialPricing {
	if p == nil {
		return nil
	}
	base := make([]service.PricingInterval, len(p.Intervals))
	for i, interval := range p.Intervals {
		base[i] = interval.PricingInterval
	}
	var intervals []modelPlazaOfficialPricingInterval
	for i, interval := range toUserPricingIntervals(base) {
		intervals = append(intervals, modelPlazaOfficialPricingInterval{interval, p.Intervals[i].CacheWrite30mPrice})
	}
	return &modelPlazaOfficialPricing{
		BillingMode: p.BillingMode, UnitPrice: p.UnitPrice,
		InputPrice:         p.InputPrice,
		OutputPrice:        p.OutputPrice,
		CacheWritePrice:    p.CacheWritePrice,
		CacheWrite1hPrice:  p.CacheWrite1hPrice,
		CacheWrite30mPrice: p.CacheWrite30mPrice,
		CacheWrite12hPrice: p.CacheWrite12hPrice,
		CacheReadPrice:     p.CacheReadPrice,
		Intervals:          intervals,
		PriceWindows:       p.PriceWindows,
	}
}
