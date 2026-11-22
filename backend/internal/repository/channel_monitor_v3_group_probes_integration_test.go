//go:build integration

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3MigrateInheritedGroupProbes(t *testing.T) {
	tx := testTx(t)
	_, err := tx.Exec(`ALTER TABLE channel_monitor_v3_config DROP CONSTRAINT channel_monitor_v3_no_shared_probe`)
	require.NoError(t, err)
	const shared = `{"endpoint":"https://shared.example","provider":"openai","primary_model":"main","api_key_encrypted":"shared-cipher","extra_models":["extra"],"interval_seconds":45}`
	const own = `{"endpoint":"https://own.example","primary_model":"own","api_key_encrypted":"own-cipher"}`
	_, err = tx.Exec(`UPDATE channel_monitor_v3_config SET version=4, probe=$1, groups=$2 WHERE id=1`, shared,
		`[{"group_id":7},{"group_id":8,"probe":`+own+`},{"group_id":9,"enabled":false,"models":["override"]}]`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("922_channel_monitor_v3_group_probes.sql")
	require.NoError(t, err)
	_, err = tx.Exec(string(migration))
	require.NoError(t, err)
	var first, second, third, override, root string
	var version int
	require.NoError(t, tx.QueryRow(`SELECT groups#>'{0,probe}', groups#>'{1,probe}', groups#>'{2,probe}',
		groups#>>'{2,models,0}', probe, version FROM channel_monitor_v3_config WHERE id=1`).Scan(&first, &second, &third, &override, &root, &version))
	require.JSONEq(t, shared, first)
	require.JSONEq(t, own, second)
	require.JSONEq(t, shared, third)
	require.Equal(t, "override", override)
	require.JSONEq(t, `{}`, root)
	require.Equal(t, 5, version)
	_, err = tx.Exec(`UPDATE channel_monitor_v3_config SET groups=jsonb_set(groups,'{0,probe,api_key_encrypted}','"rotated"') WHERE id=1`)
	require.NoError(t, err)
	_, err = tx.Exec(string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(`SELECT groups#>>'{0,probe,api_key_encrypted}', groups#>>'{2,probe,api_key_encrypted}', version
		FROM channel_monitor_v3_config WHERE id=1`).Scan(&first, &third, &version))
	require.Equal(t, "rotated", first)
	require.Equal(t, "shared-cipher", third)
	require.Equal(t, 5, version)
}
