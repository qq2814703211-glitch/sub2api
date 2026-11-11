//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

// V3 must be independent of V1/V2's global switches (ADR 0008). These tests pin
// that down from the service side: the only thing that decides whether V3 runs is
// channel_monitor_v3_config.enabled.

type channelMonitorV3RepoStub struct {
	cfg         *ChannelMonitorV3Config
	groups      map[int64]ChannelMonitorV3GroupInfo
	monitorable []ChannelMonitorV3MonitorableGroup
	cacheRates  map[int64]float64
	busy        map[int64]bool
	samples     []ChannelMonitorV3ProbeSample
	updates     int
	lastUpdate  ChannelMonitorV3Config
	conflict    bool
}

func (r *channelMonitorV3RepoStub) GetConfig(context.Context) (*ChannelMonitorV3Config, error) {
	if r.cfg == nil {
		return defaultTestV3Config(), nil
	}
	return r.cfg, nil
}

func (r *channelMonitorV3RepoStub) UpdateConfig(_ context.Context, cfg ChannelMonitorV3Config, _ int) (*ChannelMonitorV3Config, error) {
	if r.conflict {
		return nil, ErrChannelMonitorV3ConfigConflict
	}
	r.updates++
	// 深拷贝分组切片：服务层返回前会对配置做 redact（清空密文），如果 lastUpdate
	// 与返回值的 Groups 共用底层数组，redact 会把断言要看的密文一起擦掉。
	r.lastUpdate = cfg
	r.lastUpdate.Groups = cloneV3Groups(cfg.Groups)
	cfg.Version = 2
	r.cfg = &cfg
	return &cfg, nil
}

// cloneV3Groups 复制分组切片与其内部的探针配置，避免测试断言被 redact 影响。
func cloneV3Groups(groups []ChannelMonitorV3Group) []ChannelMonitorV3Group {
	if groups == nil {
		return nil
	}
	out := make([]ChannelMonitorV3Group, len(groups))
	for i, group := range groups {
		out[i] = group
		out[i].Models = append([]string{}, group.Models...)
		out[i].Probe.ExtraModels = append([]string{}, group.Probe.ExtraModels...)
	}
	return out
}

func (r *channelMonitorV3RepoStub) InsertProbeSamples(_ context.Context, samples []ChannelMonitorV3ProbeSample) error {
	r.samples = append(r.samples, samples...)
	return nil
}

func (r *channelMonitorV3RepoStub) ListProbeSamples(context.Context, time.Time, time.Time) ([]ChannelMonitorV3ProbeSample, error) {
	return r.samples, nil
}

func (r *channelMonitorV3RepoStub) LoadMonitorableGroups(context.Context, []int64) (map[int64]ChannelMonitorV3GroupInfo, error) {
	if r.groups == nil {
		return map[int64]ChannelMonitorV3GroupInfo{}, nil
	}
	return r.groups, nil
}

func (r *channelMonitorV3RepoStub) ListMonitorableGroups(context.Context) ([]ChannelMonitorV3MonitorableGroup, error) {
	return r.monitorable, nil
}

func (r *channelMonitorV3RepoStub) GroupCacheRates(context.Context, []int64, int) (map[int64]float64, error) {
	return r.cacheRates, nil
}

func (r *channelMonitorV3RepoStub) GroupHasOrganicTrafficSince(_ context.Context, groupID int64, _ time.Time) (bool, error) {
	return r.busy[groupID], nil
}

func defaultTestV3Config() *ChannelMonitorV3Config {
	return &ChannelMonitorV3Config{
		Version:                1,
		Enabled:                false,
		Groups:                 []ChannelMonitorV3Group{},
		Probe:                  ChannelMonitorV3Probe{ExtraModels: []string{}},
		CacheAverageWindowDays: ChannelMonitorV3DefaultCacheAverageWindowDays,
	}
}

type channelMonitorV3ProberStub struct {
	calls   int
	groupID int64
	models  []string
	result  []ChannelMonitorV3ProbeSample
	key     string
	keyErr  error

	// 逐组观测：记录每组拿到的模型与探针身份，以及解密的密文顺序。
	// 「每组独立探针」的核心断言全靠这几个字段 —— 只看最后一次调用无法证明
	// 别的分组也照常跑了。
	modelsByGroup map[int64][]string
	probeByGroup  map[int64]ChannelMonitorV3Probe
	callsByGroup  map[int64]int
	decryptInputs []string
	// keysByCiphertext 让某个分组的 key 解密失败，模拟「一组 key 坏了」。
	keysByCiphertext       map[string]string
	decryptErrByCiphertext map[string]error
}

func (p *channelMonitorV3ProberStub) ProbeGroup(_ context.Context, probe ChannelMonitorV3Probe, groupID int64, models []string) []ChannelMonitorV3ProbeSample {
	p.calls++
	p.groupID = groupID
	p.models = models
	if p.modelsByGroup == nil {
		p.modelsByGroup = map[int64][]string{}
	}
	if p.probeByGroup == nil {
		p.probeByGroup = map[int64]ChannelMonitorV3Probe{}
	}
	if p.callsByGroup == nil {
		p.callsByGroup = map[int64]int{}
	}
	p.modelsByGroup[groupID] = append([]string{}, models...)
	p.probeByGroup[groupID] = probe
	p.callsByGroup[groupID]++
	return p.result
}

func (p *channelMonitorV3ProberStub) DecryptAPIKey(ciphertext string) (string, error) {
	p.decryptInputs = append(p.decryptInputs, ciphertext)
	if err, ok := p.decryptErrByCiphertext[ciphertext]; ok {
		return "", err
	}
	if plain, ok := p.keysByCiphertext[ciphertext]; ok {
		return plain, nil
	}
	if p.keyErr != nil {
		return "", p.keyErr
	}
	return p.key, nil
}

// enabledV3Config is a config that is on and has one probed group.
func enabledV3Config() *ChannelMonitorV3Config {
	cfg := defaultTestV3Config()
	cfg.Enabled = true
	cfg.Groups = []ChannelMonitorV3Group{{GroupID: 7, Enabled: true, ProbeEnabled: true}}
	cfg.Probe = ChannelMonitorV3Probe{
		Endpoint:        "https://api.openai.com",
		APIKeyEncrypted: "ciphertext",
		Provider:        MonitorProviderAnthropic,
		PrimaryModel:    "claude-sonnet-4",
		ExtraModels:     []string{},
		IntervalSeconds: 300,
	}
	return cfg
}

// (a) Global channel_monitor_enabled / channel_monitor_mode are not consulted.
// The V3 runner has no dependency on the setting service at all, so turning the V1
// feature off cannot influence it. This test proves the runner probes with the
// global switches off, and that nothing but V3's own switch stops it.
func TestChannelMonitorV3ProbeRunnerIgnoresGlobalMonitorSwitches(t *testing.T) {
	repo := &channelMonitorV3RepoStub{cfg: enabledV3Config()}
	prober := &channelMonitorV3ProberStub{
		key:    "plain-key",
		result: []ChannelMonitorV3ProbeSample{{GroupID: 7, Success: true, CheckedAt: time.Now().UTC()}},
	}
	runner := NewChannelMonitorV3ProbeRunner(NewChannelMonitorV3Service(repo, nil), prober)

	// The global V1 feature switch is off and the mode is v1. V3 must not care.
	if err := runner.ProbeOnce(context.Background()); err != nil {
		t.Fatalf("V3 must run while the global channel monitor is off, got %v", err)
	}
	if prober.calls != 1 {
		t.Fatalf("expected exactly one probe round, got %d", prober.calls)
	}
	if len(repo.samples) != 1 {
		t.Fatalf("probe samples must be persisted, got %d", len(repo.samples))
	}

	// Flipping V3's own switch off is what stops it.
	repo.cfg.Enabled = false
	if err := runner.ProbeOnce(context.Background()); !errors.Is(err, ErrChannelMonitorV3ProbeDisabled) {
		t.Fatalf("V3's own switch must gate it, got %v", err)
	}
	if prober.calls != 1 {
		t.Fatalf("a disabled V3 must not probe, got %d calls", prober.calls)
	}
}

func TestChannelMonitorV3ServicePublicStatusTracksOnlyItsOwnSwitch(t *testing.T) {
	repo := &channelMonitorV3RepoStub{cfg: defaultTestV3Config()}
	svc := NewChannelMonitorV3Service(repo, nil)

	status, err := svc.PublicStatus(context.Background())
	if err != nil {
		t.Fatalf("public status: %v", err)
	}
	if status["enabled"] {
		t.Fatal("V3 must report disabled when its own switch is off")
	}

	repo.cfg = enabledV3Config()
	status, err = svc.PublicStatus(context.Background())
	if err != nil {
		t.Fatalf("public status: %v", err)
	}
	if !status["enabled"] {
		t.Fatal("V3 must report enabled from its own switch")
	}
}

// (b) V3 does not consult model_allowlist / model_pricing / platforms. The
// repository contract only exposes group name + platform, and the derived V2
// config carries an empty model list — which is exactly "no allow-list".
func TestChannelMonitorV3DeriveScopeKeepsGroupsWithoutAllowlistOrPricing(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups = []ChannelMonitorV3Group{
		{GroupID: 7, Enabled: true},
		{GroupID: 9, Enabled: true},
		{GroupID: 11, Enabled: false},
	}
	// Note the group info carries nothing but a name and a platform: there is no
	// allow-list or pricing field for V3 to filter on.
	groups := map[int64]ChannelMonitorV3GroupInfo{
		7:  {Name: "claude-main", Platform: "anthropic"},
		9:  {Name: "gpt-main", Platform: "openai"},
		11: {Name: "disabled-one", Platform: "openai"},
	}

	scope := cfg.DeriveScope(groups)
	if len(scope.GroupIDs) != 2 || scope.GroupIDs[0] != 7 || scope.GroupIDs[1] != 9 {
		t.Fatalf("only enabled groups belong in scope, got %v", scope.GroupIDs)
	}
	if len(scope.Platforms) != 2 || scope.Platforms[0] != "anthropic" || scope.Platforms[1] != "openai" {
		t.Fatalf("platforms must be derived from the selected groups, got %v", scope.Platforms)
	}
	if scope.Empty() {
		t.Fatal("a scope with two groups must not be empty")
	}

	// Unknown / deleted groups drop out instead of leaving a permanent empty card.
	groups[9] = ChannelMonitorV3GroupInfo{}
	scope = cfg.DeriveScope(groups)
	if len(scope.GroupIDs) != 2 {
		t.Fatalf("a group with an empty platform is still a group, got %v", scope.GroupIDs)
	}
	delete(groups, 9)
	scope = cfg.DeriveScope(groups)
	if len(scope.GroupIDs) != 1 || scope.GroupIDs[0] != 7 {
		t.Fatalf("a deleted group must not stay on the board, got %v", scope.GroupIDs)
	}
}

// The V3 board must refuse to serve anything while its own switch is off, and it
// must not fall back to V1/V2 data to fill the gap.
func TestChannelMonitorV3BoardRequiresItsOwnSwitch(t *testing.T) {
	repo := &channelMonitorV3RepoStub{cfg: defaultTestV3Config()}
	svc := NewChannelMonitorV3Service(repo, nil)

	if _, err := svc.Snapshot(context.Background(), ChannelMonitorV2Filter{}, true); !errors.Is(err, ErrChannelMonitorDisabled) {
		t.Fatalf("snapshot must be refused while V3 is off, got %v", err)
	}
	if _, err := svc.Matrix(context.Background(), ChannelMonitorV2Filter{}, ChannelMonitorV2GroupByPlatformGroup, true); !errors.Is(err, ErrChannelMonitorDisabled) {
		t.Fatalf("matrix must be refused while V3 is off, got %v", err)
	}
}

// (c) + (d) The probe runner must skip groups that already have organic traffic,
// and it must take the probe target list from V3's own probe identity.
func TestChannelMonitorV3ProbeRunnerSkipsGroupsWithOrganicTraffic(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups = []ChannelMonitorV3Group{
		{GroupID: 7, Enabled: true, ProbeEnabled: true},
		{GroupID: 9, Enabled: true, ProbeEnabled: true},
		{GroupID: 11, Enabled: true, ProbeEnabled: false},
	}
	repo := &channelMonitorV3RepoStub{cfg: cfg, busy: map[int64]bool{7: true}}
	prober := &channelMonitorV3ProberStub{
		key:    "plain-key",
		result: []ChannelMonitorV3ProbeSample{{GroupID: 9, Success: true, CheckedAt: time.Now().UTC()}},
	}
	runner := NewChannelMonitorV3ProbeRunner(NewChannelMonitorV3Service(repo, nil), prober)

	if err := runner.ProbeOnce(context.Background()); err != nil {
		t.Fatalf("probe once: %v", err)
	}
	if prober.calls != 1 {
		t.Fatalf("only the idle, probe-enabled group should be probed, got %d calls", prober.calls)
	}
	if prober.groupID != 9 {
		t.Fatalf("group 7 has organic traffic and group 11 has probes off; expected group 9, got %d", prober.groupID)
	}
	if len(prober.models) != 1 || prober.models[0] != "claude-sonnet-4" {
		t.Fatalf("probe models must come from the V3 probe identity, got %v", prober.models)
	}
}

func TestChannelMonitorV3GroupModelsOverrideProbeIdentity(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups = []ChannelMonitorV3Group{{
		GroupID:      7,
		Enabled:      true,
		ProbeEnabled: true,
		Models:       []string{"claude-opus-4"},
	}}
	repo := &channelMonitorV3RepoStub{cfg: cfg}
	prober := &channelMonitorV3ProberStub{key: "plain-key",
		result: []ChannelMonitorV3ProbeSample{{GroupID: 7, Success: true, CheckedAt: time.Now().UTC()}}}
	runner := NewChannelMonitorV3ProbeRunner(NewChannelMonitorV3Service(repo, nil), prober)

	if err := runner.ProbeOnce(context.Background()); err != nil {
		t.Fatalf("probe once: %v", err)
	}
	if len(prober.models) != 1 || prober.models[0] != "claude-opus-4" {
		t.Fatalf("a per-group model list must win over the identity default, got %v", prober.models)
	}
}

func TestChannelMonitorV3ProbeRunnerNeedsAKey(t *testing.T) {
	repo := &channelMonitorV3RepoStub{cfg: enabledV3Config()}
	prober := &channelMonitorV3ProberStub{keyErr: errors.New("no key")}
	runner := NewChannelMonitorV3ProbeRunner(NewChannelMonitorV3Service(repo, nil), prober)

	if err := runner.ProbeOnce(context.Background()); err == nil {
		t.Fatal("a probe round without a usable key must not silently succeed")
	}
	if prober.calls != 0 {
		t.Fatalf("no probe should run without a key, got %d", prober.calls)
	}
}

// 只有一个全局 probe、分组里没有 probe 的老配置：所有分组回落到全局那份，
// 行为与改造前逐字一致。这是向后兼容的硬要求。
func TestChannelMonitorV3GroupProbeFallsBackToGlobal(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups = []ChannelMonitorV3Group{
		{GroupID: 7, Enabled: true, ProbeEnabled: true},
		{GroupID: 9, Enabled: true, ProbeEnabled: true},
	}
	repo := &channelMonitorV3RepoStub{cfg: cfg}
	prober := &channelMonitorV3ProberStub{
		key:    "global-plain-key",
		result: []ChannelMonitorV3ProbeSample{{Success: true, CheckedAt: time.Now().UTC()}},
	}
	runner := NewChannelMonitorV3ProbeRunner(NewChannelMonitorV3Service(repo, nil), prober)

	if err := runner.ProbeOnce(context.Background()); err != nil {
		t.Fatalf("probe once: %v", err)
	}
	for _, groupID := range []int64{7, 9} {
		probe, ok := prober.probeByGroup[groupID]
		if !ok {
			t.Fatalf("group %d must be probed with the global probe identity", groupID)
		}
		if probe.Endpoint != cfg.Probe.Endpoint {
			t.Fatalf("group %d must inherit the global endpoint, got %q", groupID, probe.Endpoint)
		}
		if probe.APIKeyEncrypted != "global-plain-key" {
			t.Fatalf("group %d must use the decrypted global key, got %q", groupID, probe.APIKeyEncrypted)
		}
	}
	if len(prober.decryptInputs) == 0 || prober.decryptInputs[0] != "ciphertext" {
		t.Fatalf("the global ciphertext must be the one decrypted, got %v", prober.decryptInputs)
	}
}

// 分组级 probe 存在就整份用分组级：endpoint / key / 模型都是这组自己的。
func TestChannelMonitorV3GroupProbeOverridesGlobal(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups = []ChannelMonitorV3Group{
		{GroupID: 7, Enabled: true, ProbeEnabled: true},
		{
			GroupID:      9,
			Enabled:      true,
			ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint:        "https://api.anthropic.com",
				APIKeyEncrypted: "gpt-ciphertext",
				Provider:        MonitorProviderOpenAI,
				PrimaryModel:    "gpt-5",
				ExtraModels:     []string{"gpt-5-mini"},
				IntervalSeconds: 600,
			},
		},
	}
	repo := &channelMonitorV3RepoStub{cfg: cfg}
	prober := &channelMonitorV3ProberStub{
		keysByCiphertext: map[string]string{
			"ciphertext":     "global-plain-key",
			"gpt-ciphertext": "gpt-plain-key",
		},
		result: []ChannelMonitorV3ProbeSample{{Success: true, CheckedAt: time.Now().UTC()}},
	}
	runner := NewChannelMonitorV3ProbeRunner(NewChannelMonitorV3Service(repo, nil), prober)

	if err := runner.ProbeOnce(context.Background()); err != nil {
		t.Fatalf("probe once: %v", err)
	}

	global := prober.probeByGroup[7]
	if global.Endpoint != "https://api.openai.com" || global.APIKeyEncrypted != "global-plain-key" {
		t.Fatalf("group 7 must keep the global identity, got %+v", global)
	}
	own := prober.probeByGroup[9]
	if own.Endpoint != "https://api.anthropic.com" {
		t.Fatalf("group 9 must use its own endpoint, got %q", own.Endpoint)
	}
	if own.APIKeyEncrypted != "gpt-plain-key" {
		t.Fatalf("group 9 must use its own key, got %q", own.APIKeyEncrypted)
	}
	if own.Provider != MonitorProviderOpenAI {
		t.Fatalf("group 9 must use its own provider, got %q", own.Provider)
	}
	if got := prober.modelsByGroup[9]; len(got) != 2 || got[0] != "gpt-5" || got[1] != "gpt-5-mini" {
		t.Fatalf("group 9 must probe its own models, got %v", got)
	}
	// 组级 models 为空时用组级 probe 的 primary+extra，而不是全局的模型。
	if got := prober.modelsByGroup[7]; len(got) != 1 || got[0] != "claude-sonnet-4" {
		t.Fatalf("group 7 must probe the global identity models, got %v", got)
	}
}

// 某一个分组的 key 解不开，只跳过这一组；其余分组照常探测。
// 这是原实现最严重的缺陷：循环外解一次全局 key，失败就 return 整轮。
func TestChannelMonitorV3BrokenGroupKeyDoesNotStopOtherGroups(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups = []ChannelMonitorV3Group{
		{
			GroupID: 7, Enabled: true, ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint:        "https://broken.example",
				APIKeyEncrypted: "broken-ciphertext",
				Provider:        MonitorProviderAnthropic,
				PrimaryModel:    "claude-sonnet-4",
				IntervalSeconds: 300,
			},
		},
		{
			GroupID: 9, Enabled: true, ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint:        "https://healthy.example",
				APIKeyEncrypted: "healthy-ciphertext",
				Provider:        MonitorProviderOpenAI,
				PrimaryModel:    "gpt-5",
				IntervalSeconds: 300,
			},
		},
	}
	repo := &channelMonitorV3RepoStub{cfg: cfg}
	prober := &channelMonitorV3ProberStub{
		keysByCiphertext: map[string]string{"healthy-ciphertext": "healthy-key"},
		decryptErrByCiphertext: map[string]error{
			"broken-ciphertext": errors.New("ciphertext corrupted"),
		},
		result: []ChannelMonitorV3ProbeSample{{Success: true, CheckedAt: time.Now().UTC()}},
	}
	runner := NewChannelMonitorV3ProbeRunner(NewChannelMonitorV3Service(repo, nil), prober)

	if err := runner.ProbeOnce(context.Background()); err != nil {
		t.Fatalf("one broken group must not fail the whole round, got %v", err)
	}
	if prober.callsByGroup[7] != 0 {
		t.Fatalf("the group with a broken key must be skipped, got %d calls", prober.callsByGroup[7])
	}
	if prober.callsByGroup[9] != 1 {
		t.Fatalf("the healthy group must still be probed, got %d calls", prober.callsByGroup[9])
	}
	if len(repo.samples) == 0 {
		t.Fatal("samples from the healthy group must be persisted")
	}
}

// 每组的探测间隔各取各的：到期判断按组做，轮询周期取最小值。
func TestChannelMonitorV3RoundsHonourPerGroupIntervals(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups = []ChannelMonitorV3Group{
		{
			GroupID: 7, Enabled: true, ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint: "https://fast.example", Provider: MonitorProviderAnthropic,
				PrimaryModel: "claude-sonnet-4", IntervalSeconds: 60,
			},
		},
		{
			GroupID: 9, Enabled: true, ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint: "https://slow.example", Provider: MonitorProviderOpenAI,
				PrimaryModel: "gpt-5", IntervalSeconds: 3600,
			},
		},
	}
	repo := &channelMonitorV3RepoStub{cfg: cfg}
	prober := &channelMonitorV3ProberStub{
		key:    "plain-key",
		result: []ChannelMonitorV3ProbeSample{{Success: true, CheckedAt: time.Now().UTC()}},
	}
	runner := NewChannelMonitorV3ProbeRunner(NewChannelMonitorV3Service(repo, nil), prober)

	// 轮询周期必须是两组里最小的那个，不是最后一组的。
	if got := runner.roundInterval(cfg); got != 60*time.Second {
		t.Fatalf("the round interval must be the smallest per-group interval, got %v", got)
	}

	clock := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	runner.now = func() time.Time { return clock }
	runner.probeGroups(context.Background(), cfg, false)
	if prober.callsByGroup[7] != 1 || prober.callsByGroup[9] != 1 {
		t.Fatalf("the first round must probe both groups, got %v", prober.callsByGroup)
	}

	// 30 秒后：60s 的组还没到期，3600s 的组也没到期。
	clock = clock.Add(30 * time.Second)
	runner.probeGroups(context.Background(), cfg, false)
	if prober.callsByGroup[7] != 1 || prober.callsByGroup[9] != 1 {
		t.Fatalf("no group is due after 30s, got %v", prober.callsByGroup)
	}

	// 再过 40 秒（累计 70s）：只有 60s 的组到期。
	clock = clock.Add(40 * time.Second)
	runner.probeGroups(context.Background(), cfg, false)
	if prober.callsByGroup[7] != 2 {
		t.Fatalf("the 60s group must be probed again at 70s, got %v", prober.callsByGroup)
	}
	if prober.callsByGroup[9] != 1 {
		t.Fatalf("the 3600s group must not be re-probed at 70s, got %v", prober.callsByGroup)
	}

	// 一小时后：3600s 的组才到期。
	clock = clock.Add(3600 * time.Second)
	runner.probeGroups(context.Background(), cfg, false)
	if prober.callsByGroup[9] != 2 {
		t.Fatalf("the 3600s group must be probed after an hour, got %v", prober.callsByGroup)
	}
}
