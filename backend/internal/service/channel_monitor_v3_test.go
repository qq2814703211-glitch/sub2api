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
	cfg        *ChannelMonitorV3Config
	groups     map[int64]ChannelMonitorV3GroupInfo
	monitorable []ChannelMonitorV3MonitorableGroup
	cacheRates map[int64]float64
	busy       map[int64]bool
	samples    []ChannelMonitorV3ProbeSample
	updates    int
	lastUpdate ChannelMonitorV3Config
	conflict   bool
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
	r.lastUpdate = cfg
	cfg.Version = 2
	r.cfg = &cfg
	return &cfg, nil
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
}

func (p *channelMonitorV3ProberStub) ProbeGroup(_ context.Context, _ ChannelMonitorV3Probe, groupID int64, models []string) []ChannelMonitorV3ProbeSample {
	p.calls++
	p.groupID = groupID
	p.models = models
	return p.result
}

func (p *channelMonitorV3ProberStub) DecryptAPIKey(string) (string, error) {
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
		Endpoint:        "https://upstream.example",
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
