//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// L1-3：缓存率均值窗口支持「分钟 or 天」。
//
// 三条不变量：
//   - 老配置（只有 cache_average_window_days）仍按天解释，行为与升级前一致；
//   - 分钟粒度真的换算到下游使用的分钟数上，不是 UI 换了个单位；
//   - 上限只有一个物理值（90 天 = 129600 分钟），切换单位不会把合法值变成非法值。

func windowTestConfig(days int, value int, unit string) *ChannelMonitorV3Config {
	cfg := enabledV3Config()
	cfg.CacheAverageWindowDays = days
	cfg.CacheAverageWindowValue = value
	cfg.CacheAverageWindowUnit = unit
	return cfg
}

// 老配置：只有 days 字段，unit/value 都是零值。
func TestChannelMonitorV3LegacyWindowStillMeansDays(t *testing.T) {
	cfg := windowTestConfig(30, 0, "")
	window := cfg.CacheWindow()
	if window.Value != 30 || window.Unit != ChannelMonitorV3WindowUnitDay {
		t.Fatalf("a legacy days-only config must mean 30 days, got %+v", window)
	}
	if got := cfg.CacheAverageWindowMinutes(); got != 30*24*60 {
		t.Fatalf("a legacy 30-day window must be 43200 minutes downstream, got %d", got)
	}
	if days := window.WholeDays(); days != 30 {
		t.Fatalf("30 days must stay on the daily rollup fast path, got %d whole days", days)
	}
}

func TestChannelMonitorV3MinuteWindowConvertsToMinutes(t *testing.T) {
	cfg := windowTestConfig(0, 30, ChannelMonitorV3WindowUnitMinute)
	if got := cfg.CacheAverageWindowMinutes(); got != 30 {
		t.Fatalf("a 30-minute window must reach the query as 30 minutes, got %d", got)
	}
	if days := cfg.CacheWindow().WholeDays(); days != 0 {
		t.Fatalf("a 30-minute window is not a whole number of days, got %d", days)
	}
}

func TestChannelMonitorV3DayWindowConvertsToMinutes(t *testing.T) {
	cfg := windowTestConfig(0, 2, ChannelMonitorV3WindowUnitDay)
	if got := cfg.CacheAverageWindowMinutes(); got != 2*24*60 {
		t.Fatalf("a 2-day window must be 2880 minutes, got %d", got)
	}
	if days := cfg.CacheWindow().WholeDays(); days != 2 {
		t.Fatalf("a 2-day window must keep the daily rollup fast path, got %d", days)
	}
}

// 上限：90 天合法，90 天 + 1 分钟非法；1 天按分钟表达（1440）合法。
func TestChannelMonitorV3WindowUpperBoundIsOnePhysicalLimit(t *testing.T) {
	cases := []struct {
		name    string
		value   int
		unit    string
		wantErr bool
	}{
		{"90 days", 90, ChannelMonitorV3WindowUnitDay, false},
		{"91 days", 91, ChannelMonitorV3WindowUnitDay, true},
		{"129600 minutes", 129600, ChannelMonitorV3WindowUnitMinute, false},
		{"129601 minutes", 129601, ChannelMonitorV3WindowUnitMinute, true},
		{"1 day as 1440 minutes", 1440, ChannelMonitorV3WindowUnitMinute, false},
		{"zero value", 0, ChannelMonitorV3WindowUnitMinute, true},
		{"bogus unit", 5, "hour", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := windowTestConfig(0, tc.value, tc.unit)
			err := normalizeChannelMonitorV3Config(cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("%s must be rejected", tc.name)
				}
				if !errors.Is(err, ErrChannelMonitorV3InvalidConfig) {
					t.Fatalf("%s must fail with ErrChannelMonitorV3InvalidConfig, got %v", tc.name, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s must be accepted, got %v", tc.name, err)
			}
		})
	}
}

// 切单位不该顺手改数值：「90 分钟」保存后仍是 90 分钟，不会被夹成 1 天或重置成默认 30 天。
func TestChannelMonitorV3WindowUnitSwitchDoesNotResetValue(t *testing.T) {
	cfg := windowTestConfig(0, 90, ChannelMonitorV3WindowUnitMinute)
	if err := normalizeChannelMonitorV3Config(cfg); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if cfg.CacheAverageWindowValue != 90 || cfg.CacheAverageWindowUnit != ChannelMonitorV3WindowUnitMinute {
		t.Fatalf("the minute window must survive normalization untouched, got %d %q",
			cfg.CacheAverageWindowValue, cfg.CacheAverageWindowUnit)
	}
	// 老列必须继续写一个能过 CHECK(1..90) 的天数：90 分钟向上取整成 1 天。
	if cfg.CacheAverageWindowDays != 1 {
		t.Fatalf("the legacy days column must hold the rounded-up day count (1), got %d", cfg.CacheAverageWindowDays)
	}

	// 切到天单位、填 3：数值 3 与单位 day 都保留。
	switched := windowTestConfig(0, 3, ChannelMonitorV3WindowUnitDay)
	if err := normalizeChannelMonitorV3Config(switched); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if switched.CacheAverageWindowValue != 3 || switched.CacheAverageWindowUnit != ChannelMonitorV3WindowUnitDay {
		t.Fatalf("a day window must keep its own value, got %d %q",
			switched.CacheAverageWindowValue, switched.CacheAverageWindowUnit)
	}
	if switched.CacheAverageWindowDays != 3 {
		t.Fatalf("the legacy column must mirror whole days, got %d", switched.CacheAverageWindowDays)
	}
}

// 90 小时不是整天数：不能静默取整成 4 天，必须走 usage_logs 直扫。
func TestChannelMonitorV3NonWholeDayWindowBypassesDailyRollup(t *testing.T) {
	cfg := windowTestConfig(0, 90*60, ChannelMonitorV3WindowUnitMinute)
	window := cfg.CacheWindow()
	if days := window.WholeDays(); days != 0 {
		t.Fatalf("90 hours is not a whole number of days, got %d", days)
	}
	if minutes := cfg.CacheAverageWindowMinutes(); minutes != 90*60 {
		t.Fatalf("90 hours must stay 5400 minutes, got %d", minutes)
	}
}

// 窗口经 DeriveScope 落到看板 scope 上，单位与数值都不能丢。
func TestChannelMonitorV3DeriveScopeCarriesMinuteWindow(t *testing.T) {
	cfg := windowTestConfig(0, 45, ChannelMonitorV3WindowUnitMinute)
	cfg.Groups = []ChannelMonitorV3Group{{GroupID: 7, Enabled: true}}
	scope := cfg.DeriveScope(map[int64]ChannelMonitorV3GroupInfo{
		7: {Name: "claude-main", Platform: "anthropic"},
	})
	if scope.CacheAverageWindowMinutes != 45 {
		t.Fatalf("the board scope must carry the 45-minute window, got %d", scope.CacheAverageWindowMinutes)
	}
}

func TestChannelMonitorV3GroupProbeNormalization(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups[0].Probe = ChannelMonitorV3Probe{}
	if err := normalizeChannelMonitorV3Config(cfg); !errors.Is(err, ErrChannelMonitorV3InvalidConfig) || !strings.Contains(err.Error(), "group 7") {
		t.Fatalf("active group without identity: %v", err)
	}
	cfg.Groups[0].ProbeEnabled = false
	if err := normalizeChannelMonitorV3Config(cfg); err != nil {
		t.Fatalf("passive-only group should not need an identity: %v", err)
	}

	// 只填 endpoint、没有 model：无法独立成一次探测，必须拒绝。
	broken := enabledV3Config()
	broken.Groups = []ChannelMonitorV3Group{{
		GroupID: 7, Enabled: true, ProbeEnabled: true,
		Probe: ChannelMonitorV3Probe{Endpoint: "https://api.openai.com"},
	}}
	if err := normalizeChannelMonitorV3Config(broken); err == nil {
		t.Fatal("a half-configured group probe must be rejected, not silently half-inherited")
	}

	// 完整的组级 probe：provider / interval 补默认值，并被识别为「自带身份」。
	full := enabledV3Config()
	full.Groups = []ChannelMonitorV3Group{{
		GroupID: 7, Enabled: true, ProbeEnabled: true,
		Probe: ChannelMonitorV3Probe{
			Endpoint:     "https://api.openai.com/",
			PrimaryModel: "  gpt-5  ",
		},
	}}
	if err := normalizeChannelMonitorV3Config(full); err != nil {
		t.Fatalf("a complete group probe must normalize: %v", err)
	}
	probe := full.Groups[0].Probe
	if probe.Endpoint != "https://api.openai.com" {
		t.Fatalf("the endpoint must drop the trailing slash, got %q", probe.Endpoint)
	}
	if probe.PrimaryModel != "gpt-5" {
		t.Fatalf("the model must be trimmed, got %q", probe.PrimaryModel)
	}
	if probe.Provider != MonitorProviderAnthropic {
		t.Fatalf("a missing provider must default, got %q", probe.Provider)
	}
	if probe.IntervalSeconds != ChannelMonitorV3DefaultIntervalSeconds {
		t.Fatalf("a missing interval must default, got %d", probe.IntervalSeconds)
	}
	if !full.Groups[0].HasOwnProbe() {
		t.Fatal("a complete group probe must be treated as its own identity")
	}
}

// 响应里不能回传任何密文；每组的 api_key_set 必须逐组给出。
func TestChannelMonitorV3RedactHidesEveryGroupKey(t *testing.T) {
	cfg := enabledV3Config()
	cfg.Groups = []ChannelMonitorV3Group{
		{
			GroupID: 7, Enabled: true, ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint: "https://api.openai.com", APIKeyEncrypted: "own-ciphertext",
				PrimaryModel: "gpt-5", Provider: MonitorProviderOpenAI, IntervalSeconds: 300,
			},
		},
		{GroupID: 9, Enabled: true, ProbeEnabled: true},
	}
	redacted := redactChannelMonitorV3Config(cfg)

	for _, group := range redacted.Groups {
		if group.Probe.APIKeyEncrypted != "" {
			t.Fatalf("group %d ciphertext must never be returned, got %q",
				group.GroupID, group.Probe.APIKeyEncrypted)
		}
	}
	if !redacted.Groups[0].APIKeySet {
		t.Fatal("a group with a stored key must report api_key_set=true")
	}
	if redacted.Groups[1].APIKeySet {
		t.Fatal("a group without its own key must report api_key_set=false")
	}
}

// 保存时：分组级明文 key 被加密写进该组，未提供 key 的分组保留原密文。
func TestChannelMonitorV3UpdateConfigKeepsPerGroupKeys(t *testing.T) {
	enc := stubV3Encryptor{prefix: "enc:"}
	existing := enabledV3Config()
	existing.Groups = []ChannelMonitorV3Group{
		{
			GroupID: 7, Enabled: true, ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint: "https://api.openai.com", APIKeyEncrypted: "enc:old-group-key",
				PrimaryModel: "gpt-5", Provider: MonitorProviderOpenAI, IntervalSeconds: 300,
			},
		},
	}
	repo := &channelMonitorV3RepoStub{cfg: existing}
	svc := NewChannelMonitorV3Service(repo, enc)

	// 只改间隔、不填 key：原密文必须保留。
	next := *existing
	next.Groups = []ChannelMonitorV3Group{
		{
			GroupID: 7, Enabled: true, ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint: "https://api.openai.com", PrimaryModel: "gpt-5",
				Provider: MonitorProviderOpenAI, IntervalSeconds: 600,
			},
		},
	}
	if _, err := svc.UpdateConfig(context.Background(), next, nil, existing.Version, 1); err != nil {
		t.Fatalf("update without a new key: %v", err)
	}
	if got := repo.lastUpdate.Groups[0].Probe.APIKeyEncrypted; got != "enc:old-group-key" {
		t.Fatalf("an unchanged group must keep its stored key, got %q", got)
	}
	if repo.lastUpdate.Groups[0].Probe.IntervalSeconds != 600 {
		t.Fatalf("the new interval must be saved, got %d", repo.lastUpdate.Groups[0].Probe.IntervalSeconds)
	}

	// 填了新 key：加密后写进该组，不动其他分组的密文。
	next2 := next
	next2.Groups = []ChannelMonitorV3Group{
		{
			GroupID: 7, Enabled: true, ProbeEnabled: true,
			Probe: ChannelMonitorV3Probe{
				Endpoint: "https://api.openai.com", PrimaryModel: "gpt-5",
				Provider: MonitorProviderOpenAI, IntervalSeconds: 600,
			},
		},
	}
	if _, err := svc.UpdateConfig(context.Background(), next2,
		map[int64]string{7: "brand-new-key"}, repo.lastUpdate.Version, 1); err != nil {
		t.Fatalf("update with a new group key: %v", err)
	}
	if got := repo.lastUpdate.Groups[0].Probe.APIKeyEncrypted; got != "enc:brand-new-key" {
		t.Fatalf("the new group key must be encrypted and stored, got %q", got)
	}
}

func TestChannelMonitorV3CannotClearActiveGroupIdentity(t *testing.T) {
	existing := enabledV3Config()
	repo := &channelMonitorV3RepoStub{cfg: existing}
	next := *existing
	next.Groups = []ChannelMonitorV3Group{{GroupID: 7, Enabled: true, ProbeEnabled: true}}
	_, err := NewChannelMonitorV3Service(repo, nil).UpdateConfig(context.Background(), next, nil, existing.Version, 1)
	if !errors.Is(err, ErrChannelMonitorV3InvalidConfig) || repo.updates != 0 {
		t.Fatalf("missing active probe must not overwrite stored config: %v", err)
	}
}

// stubV3Encryptor 让 service 测试能看清「哪条明文被加密进了哪一格」。
type stubV3Encryptor struct {
	prefix string
}

func (e stubV3Encryptor) Encrypt(plain string) (string, error) {
	return e.prefix + plain, nil
}

func (e stubV3Encryptor) Decrypt(ciphertext string) (string, error) {
	return strings.TrimPrefix(ciphertext, e.prefix), nil
}
