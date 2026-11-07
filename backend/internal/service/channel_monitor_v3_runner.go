package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ChannelMonitorV3ProbeRunner 是 V3 自己的探针执行器。
//
// 它只受 channel_monitor_v3_config.enabled 控制：
//   - 不读 channel_monitor_enabled / channel_monitor_mode（V1/V2 的全局开关与模式），
//     所以关掉 V1 的探针不会顺带改 V3 的行为，反之亦然；
//   - 不查 channel_monitors 表取身份（探针身份在 V3 设置页里单独配一条 key）；
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
	// DecryptAPIKey 解开存储的探针 key；解不开时返回错误（runner 记日志并跳过本轮）。
	DecryptAPIKey(ciphertext string) (string, error)
}

// NewChannelMonitorV3ProbeRunner 创建 V3 探针执行器。
func NewChannelMonitorV3ProbeRunner(svc *ChannelMonitorV3Service, prober ChannelMonitorV3Prober) *ChannelMonitorV3ProbeRunner {
	return &ChannelMonitorV3ProbeRunner{
		service: svc,
		probe:   prober,
		stopCh:  make(chan struct{}),
	}
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
	if r.probe == nil || len(cfg.Probe.ProbeModels()) == 0 {
		return ChannelMonitorV3DisabledIntervalFallback
	}
	plainKey, err := r.probe.DecryptAPIKey(cfg.Probe.APIKeyEncrypted)
	if err != nil || plainKey == "" {
		slog.Warn("channel_monitor_v3: probe api key unavailable, skipping round",
			"configured", cfg.APIKeySet, "error", err)
		return ChannelMonitorV3DisabledIntervalFallback
	}
	probe := cfg.Probe
	probe.APIKeyEncrypted = plainKey

	interval := time.Duration(probe.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Duration(ChannelMonitorV3DefaultIntervalSeconds) * time.Second
	}
	r.probeGroups(ctx, cfg, probe, interval)
	return interval
}

func (r *ChannelMonitorV3ProbeRunner) probeGroups(
	ctx context.Context,
	cfg *ChannelMonitorV3Config,
	probe ChannelMonitorV3Probe,
	interval time.Duration,
) {
	// 只看 V3 自己的分组名单与 probe_enabled 标记。
	since := time.Now().UTC().Add(-interval)
	samples := make([]ChannelMonitorV3ProbeSample, 0, len(cfg.Groups))
	for _, group := range cfg.ProbeGroups() {
		if ctx.Err() != nil {
			return
		}
		// 该分组已经有真实流量：桶里已有真实样本，探针只会烧配额并给页面注入一份
		// 不属于流量的数据。查不到（DB 抖动）就照常探测 —— 宁可多一次探测，
		// 也不要让 V3 因为一次查询失败而整块不更新。
		busy, err := r.service.repo.GroupHasOrganicTrafficSince(ctx, group.GroupID, since)
		if err != nil {
			slog.Warn("channel_monitor_v3: organic traffic check failed",
				"group_id", group.GroupID, "error", err)
		}
		if err == nil && busy {
			continue
		}
		models := probe.ProbeModels()
		if len(group.Models) > 0 {
			models = group.Models
		}
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

// ErrChannelMonitorV3ProbeDisabled 在 V3 关闭时由 ProbeOnce 返回。
var ErrChannelMonitorV3ProbeDisabled = errors.New("channel monitor v3 is disabled")

// ProbeOnce 立即执行一轮探测（管理页「立即探测」/ 测试用）。
//
// 与 runOnce 相同的门控：只受 V3 自己的 enabled 控制。
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
	plainKey, err := r.probe.DecryptAPIKey(cfg.Probe.APIKeyEncrypted)
	if err != nil || plainKey == "" {
		return ErrChannelMonitorMissingAPIKey
	}
	probe := cfg.Probe
	probe.APIKeyEncrypted = plainKey
	interval := time.Duration(probe.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Duration(ChannelMonitorV3DefaultIntervalSeconds) * time.Second
	}
	r.probeGroups(ctx, cfg, probe, interval)
	return nil
}
