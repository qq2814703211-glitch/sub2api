package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sqlStatementsOnly drops `--` comment lines and collapses whitespace, so a test
// can assert on what the migration actually executes.
func sqlStatementsOnly(sql string) string {
	lines := strings.Split(sql, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(strings.Fields(strings.Join(kept, " ")), " ")
}

func TestChannelMonitorV3ConfigMigration(t *testing.T) {
	content, err := FS.ReadFile("915_channel_monitor_v3_config.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// Single-row table: the whole point is one V3 config, not a per-group one.
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS channel_monitor_v3_config")
	require.Contains(t, sql, "id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1)")

	// V3's own switch plus the four configuration surfaces from the brief.
	require.Contains(t, sql, "enabled BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "groups JSONB NOT NULL DEFAULT '[]'::jsonb")
	require.Contains(t, sql, "probe JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "cache_average_window_days INTEGER NOT NULL DEFAULT 30")

	// ADR 0009: the window is bounded, not unbounded.
	require.Contains(t, sql, "CHECK (cache_average_window_days BETWEEN 1 AND 90)")

	// Optimistic locking so two operators cannot silently clobber each other.
	require.Contains(t, sql, "version INTEGER NOT NULL DEFAULT 1")

	// Idempotent seed: re-running the migration must not duplicate the row.
	require.Contains(t, sql, "WHERE NOT EXISTS (SELECT 1 FROM channel_monitor_v3_config WHERE id = 1)")
}

// The seed must copy what V1/V2 actually run today, so an upgrade needs no manual
// re-configuration. This is the one place V3 is allowed to read V1/V2 tables.
func TestChannelMonitorV3ConfigMigrationSeedsFromExistingConfig(t *testing.T) {
	content, err := FS.ReadFile("915_channel_monitor_v3_config.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// Group list comes from V2's group_ids.
	require.Contains(t, sql, "SELECT group_ids FROM channel_monitor_v2_config WHERE id = 1")
	// Probe identity comes from the effective V1 monitor row.
	require.Contains(t, sql, "FROM channel_monitors m")
	require.Contains(t, sql, "WHERE m.enabled")
	// A quota-only monitor cannot drive an active probe.
	require.Contains(t, sql, "COALESCE(m.check_mode, 'probe') <> 'quota'")
	// The switch is copied from the effective runtime mode: a hybrid deployment
	// keeps probing, everything else starts with V3 off.
	require.Contains(t, sql, "LOWER(TRIM(value)) = 'v3'")
	require.Contains(t, sql, "FROM settings WHERE key = 'channel_monitor_mode'")
}

func TestChannelMonitorV3ProbeSamplesMigration(t *testing.T) {
	content, err := FS.ReadFile("916_channel_monitor_v3_probe_samples.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS channel_monitor_v3_probe_samples")
	require.Contains(t, sql, "group_id BIGINT NOT NULL")
	require.Contains(t, sql, "status VARCHAR(20) NOT NULL")
	require.Contains(t, sql, "ttft_ms INTEGER")
	require.Contains(t, sql, "checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_probe_samples_group_time")

	// ADR 0009: the probe sample table must not carry a cache-rate column. Storing
	// one would re-introduce the fabricated 100% under a different name.
	require.NotContains(t, sql, "cache_rate")
	require.NotContains(t, sql, "cache_read_tokens")
	require.NotContains(t, sql, "cache_creation_tokens")
}

// V3 lives in its own tables. These assertions are the structural guard that V3
// never reads V1's probe history or writes into V2's config at runtime.
func TestChannelMonitorV3MigrationsKeepStorageSeparate(t *testing.T) {
	config, err := FS.ReadFile("915_channel_monitor_v3_config.sql")
	require.NoError(t, err)
	samples, err := FS.ReadFile("916_channel_monitor_v3_probe_samples.sql")
	require.NoError(t, err)

	// Check the executable part only: the header comment names the old table on
	// purpose, explaining why V3 does not use it.
	statements := sqlStatementsOnly(string(samples))
	require.NotContains(t, statements, "channel_monitor_histories")
	require.NotContains(t, statements, "channel_monitors")

	// The config table only *reads* V1/V2 in the seed; it must not write to them.
	configSQL := sqlStatementsOnly(string(config))
	require.NotContains(t, configSQL, "UPDATE channel_monitors")
	require.NotContains(t, configSQL, "UPDATE channel_monitor_v2_config")
	require.NotContains(t, configSQL, "INSERT INTO channel_monitor_v2_config")
}
