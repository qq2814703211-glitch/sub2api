package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ChannelMonitorV3ProbeRunner 是 V3 自己的探针执行器。
//
// 它只受 channel_monitor_v3_config.enabled 控制：
//   - 不读 channel_monitor_enabled / channel_monitor_mode（V1/V2 的全局开关与模式），
//     所以关掉 V1 的探针不会顺带改 V3 的行为，反之亦然；
//   - 不查 channel_monitors 表取身份（各分组在 V3 设置页单独配置探针）；
//   - 分组来源只有 channel_monitor_v3_config.groups，不看分组的
//     model_allowlist / model_pricing / platforms。
//
// 这与 ADR 0008 一致：V3 是独立产品，不是 V1/V2 的一个 mode。
type ChannelMonitorV3ProbeRunner struct {
	service *ChannelMonitorV3Service
	// probe 执行一次真实探测；由 wire 注入 ChannelMonitorV3Prober，测试里替换。
	probe   ChannelMonitorV3Prober
	stopCh  chan struct{}
	stopOne sync.Once

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// lastProbe 记录每个分组上次实际探测的时刻。
	//
	// 分组各有各的 interval，而调度器一次只能返回一个等待时长，所以循环周期取
	// 「所有分组 interval 的最小值」，再逐组判断自己到期没有。没有这张表就只能
	// 让最频繁的那组拖着所有组一起按它自己的间隔探测，慢的那组会被打爆。
	lastProbeMu sync.Mutex
	lastProbe   map[int64]time.Time
	// now 便于测试注入时间。
	now func() time.Time
}

// ChannelMonitorV3ProbeTarget 是一次探测的目标分组与模型集合。
type ChannelMonitorV3ProbeTarget struct {
	GroupID int64
	Models  []string
}

// ChannelMonitorV3Prober 执行一次上游探测并返回样本。
type ChannelMonitorV3Prober interface {
	// ProbeGroup 对一个分组打一次探测（primary + extra 模型），返回每个模型的结果。
	ProbeGroup(ctx context.Context, probe ChannelMonitorV3Probe, groupID int64, models []string) []ChannelMonitorV3ProbeSample
	// DecryptAPIKey 解开存储的探针 key；解不开时返回错误。
	DecryptAPIKey(ciphertext string) (string, error)
}

// NewChannelMonitorV3ProbeRunner 创建 V3 探针执行器。
func NewChannelMonitorV3ProbeRunner(svc *ChannelMonitorV3Service, prober ChannelMonitorV3Prober) *ChannelMonitorV3ProbeRunner {
	return &ChannelMonitorV3ProbeRunner{
		service:   svc,
		probe:     prober,
		stopCh:    make(chan struct{}),
		lastProbe: map[int64]time.Time{},
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// clock 返回 runner 的时间源（未注入时为 UTC 当前时间）。
func (r *ChannelMonitorV3ProbeRunner) clock() time.Time {
	if r.now == nil {
		return time.Now().UTC()
	}
	return r.now()
}

// due 报告该分组这一轮是否到期。
func (r *ChannelMonitorV3ProbeRunner) due(groupID int64, interval time.Duration) bool {
	if interval <= 0 {
		return true
	}
	r.lastProbeMu.Lock()
	defer r.lastProbeMu.Unlock()
	last, ok := r.lastProbe[groupID]
	if !ok {
		return true
	}
	return !r.clock().Before(last.Add(interval))
}

// markProbed 记录该分组刚探测过的时刻。
func (r *ChannelMonitorV3ProbeRunner) markProbed(groupID int64, at time.Time) {
	r.lastProbeMu.Lock()
	defer r.lastProbeMu.Unlock()
	if r.lastProbe == nil {
		r.lastProbe = map[int64]time.Time{}
	}
	r.lastProbe[groupID] = at
}

// roundInterval 返回下一轮的等待时长：所有已到期候选分组的 interval 最小值。
//
// 取最小值而不是最后一组的：慢分组（3600s）不该被快分组（60s）拖着每 60 秒打一次，
// 快分组也不该因为排在慢分组后面被降频。没有任何可用分组时回落成固定间隔。
func (r *ChannelMonitorV3ProbeRunner) roundInterval(cfg *ChannelMonitorV3Config) time.Duration {
	best := time.Duration(0)
	for _, group := range cfg.ProbeGroups() {
		seconds := group.EffectiveIntervalSeconds()
		interval := time.Duration(seconds) * time.Second
		if best == 0 || interval < best {
			best = interval
		}
	}
	if best <= 0 {
		return ChannelMonitorV3DisabledIntervalFallback
	}
	return best
}

// Start 启动 V3 探针循环。
func (r *ChannelMonitorV3ProbeRunner) Start() {
	if r == nil || r.service == nil || r.probe == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(1)
	go r.loop(ctx)
}

// Stop 停止 V3 探针循环。
func (r *ChannelMonitorV3ProbeRunner) Stop() {
	if r == nil {
		return
	}
	r.stopOne.Do(func() { close(r.stopCh) })
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}

func (r *ChannelMonitorV3ProbeRunner) loop(ctx context.Context) {
	defer r.wg.Done()
	for {
		interval := r.runOnce(ctx)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// runOnce 跑一轮探测并返回下一轮的等待时长。
//
// 关掉 V3 时什么都不做（只等一个固定间隔再检查），这是它与 V1 runner 的关键区别：
// V1 runner 的 fire 里读的是全局开关，V3 这里只读自己的 enabled。
func (r *ChannelMonitorV3ProbeRunner) runOnce(ctx context.Context) time.Duration {
	cfg, err := r.service.repo.GetConfig(ctx)
	if err != nil {
		slog.Warn("channel_monitor_v3: load config failed", "error", err)
		return ChannelMonitorV3DisabledIntervalFallback
	}
	if cfg == nil || !cfg.Enabled {
		return ChannelMonitorV3DisabledIntervalFallback
	}
	if r.probe == nil {
		return ChannelMonitorV3DisabledIntervalFallback
	}
	interval := r.roundInterval(cfg)
	r.probeGroups(ctx, cfg, false)
	return interval
}

// probeGroups 逐组探测：每组用自己的探针身份、模型与间隔。
//
// 关键改动（原实现是「一个 key 通吃所有分组」）：解密、模型解析、到期判断全部挪进
// 循环。原实现在循环外解密一次全局 key，解不开就 return 整轮 —— 一个分组配错
// key 会让其余所有分组统统停摆，这是用户报「每组的 key 不一样，不能统一用 1 个」
// 时最容易踩到的连带故障。
//
// force=true 时忽略各组的 interval（管理页「立即探测」的语义是马上都探一次）。
func (r *ChannelMonitorV3ProbeRunner) probeGroups(
	ctx context.Context,
	cfg *ChannelMonitorV3Config,
	force bool,
) {
	samples := make([]ChannelMonitorV3ProbeSample, 0, len(cfg.Groups))
	forcedAt := r.clock()
	for _, group := range cfg.ProbeGroups() {
		if ctx.Err() != nil {
			break
		}
		probe, ok := r.resolveGroupProbe(group)
		if !ok {
			continue
		}
		models := probe.ProbeModels()
		if len(group.Models) > 0 {
			models = group.Models
		}
		if len(models) == 0 {
			slog.Warn("channel_monitor_v3: group has no probe model, skipping",
				"group_id", group.GroupID)
			continue
		}
		groupInterval := time.Duration(probe.IntervalSeconds) * time.Second
		if !force && !r.due(group.GroupID, groupInterval) {
			continue
		}
		// 该分组已经有真实流量：桶里已有真实样本，探针只会烧配额并给页面注入一份
		// 不属于流量的数据。查不到（DB 抖动）就照常探测 —— 宁可多一次探测，
		// 也不要让 V3 因为一次查询失败而整块不更新。
		busy, err := r.service.repo.GroupHasOrganicTrafficSince(ctx, group.GroupID, forcedAt.Add(-groupInterval))
		if err != nil {
			slog.Warn("channel_monitor_v3: organic traffic check failed",
				"group_id", group.GroupID, "error", err)
		}
		if err == nil && busy {
			continue
		}
		r.markProbed(group.GroupID, r.clock())
		results := r.probe.ProbeGroup(ctx, probe, group.GroupID, models)
		if len(results) == 0 {
			slog.Warn("channel_monitor_v3: probe produced no sample", "group_id", group.GroupID)
			continue
		}
		samples = append(samples, results...)
	}
	if len(samples) == 0 {
		return
	}
	if err := r.service.repo.InsertProbeSamples(ctx, samples); err != nil {
		slog.Error("channel_monitor_v3: persist probe samples failed", "error", err)
	}
}

// resolveGroupProbe 取该分组生效的探针身份，并把它自己的 key 解成明文。
//
// 返回 false 表示这一组本轮跳过（没配身份、没配 key、或 key 解不开）。这里必须
// 只跳过当前组：把「key 不可用」当成轮次级错误就是原实现的整轮停摆。
func (r *ChannelMonitorV3ProbeRunner) resolveGroupProbe(
	group ChannelMonitorV3Group,
) (ChannelMonitorV3Probe, bool) {
	probe := group.Probe
	if !group.HasOwnProbe() {
		slog.Warn("channel_monitor_v3: group has no probe identity, skipping",
			"group_id", group.GroupID)
		return ChannelMonitorV3Probe{}, false
	}
	plainKey, err := r.probe.DecryptAPIKey(probe.APIKeyEncrypted)
	if err != nil || plainKey == "" {
		slog.Warn("channel_monitor_v3: group probe api key unavailable, skipping group",
			"group_id", group.GroupID, "configured", group.APIKeySet, "error", err)
		return ChannelMonitorV3Probe{}, false
	}
	probe.APIKeyEncrypted = plainKey
	if probe.IntervalSeconds <= 0 {
		probe.IntervalSeconds = ChannelMonitorV3DefaultIntervalSeconds
	}
	if strings.TrimSpace(probe.Provider) == "" {
		probe.Provider = MonitorProviderAnthropic
	}
	return probe, true
}

// ErrChannelMonitorV3ProbeDisabled 在 V3 关闭时由 ProbeOnce 返回。
var ErrChannelMonitorV3ProbeDisabled = errors.New("channel monitor v3 is disabled")

// ProbeOnce 立即执行一轮探测（管理页「立即探测」/ 测试用）。
//
// 与 runOnce 相同的门控：只受 V3 自己的 enabled 控制。它强制忽略各组 interval
// （「立即探测」就是要马上都探一次），并按组统计结果：只要有任意一组真跑过就算
// 成功。全部组都因缺 key / 缺身份被跳过时返回 ErrChannelMonitorMissingAPIKey，
// 否则页面会显示「已触发一轮探测」而实际什么都没打出去。
func (r *ChannelMonitorV3ProbeRunner) ProbeOnce(ctx context.Context) error {
	if r == nil || r.service == nil || r.probe == nil {
		return ErrChannelMonitorV3ProbeDisabled
	}
	cfg, err := r.service.repo.GetConfig(ctx)
	if err != nil {
		return err
	}
	if cfg == nil || !cfg.Enabled {
		return ErrChannelMonitorV3ProbeDisabled
	}
	if !r.hasUsableProbeGroup(cfg) {
		return ErrChannelMonitorMissingAPIKey
	}
	r.probeGroups(ctx, cfg, true)
	return nil
}

// hasUsableProbeGroup 报告是否至少有一个分组能凑出可用的探针身份。
//
// 只看「配置层面能不能跑」：endpoint + model 齐备且 key 能解开。这里不关心本轮
// 是否真的打了上游（有真实流量时会跳过），那是执行结果，不是配置可用性。
func (r *ChannelMonitorV3ProbeRunner) hasUsableProbeGroup(cfg *ChannelMonitorV3Config) bool {
	for _, group := range cfg.ProbeGroups() {
		if _, ok := r.resolveGroupProbe(group); ok {
			return true
		}
	}
	return false
}
