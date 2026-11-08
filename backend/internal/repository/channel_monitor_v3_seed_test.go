//go:build unit

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Requirement (b): a group with no model allow-list and no pricing card must still
// seed a row on the V3 board.
//
// The seeding path is shared with V2, so this test drives the real seeder with the
// config V3 derives. If V3 ever started filtering on allow-lists or pricing, the
// derived config would carry models or the group would be dropped, and this test
// would lose its rows.
func TestChannelMonitorV3SeedsRowsWithoutAllowlistOrPricing(t *testing.T) {
	scope := service.ChannelMonitorV3BoardScope{
		GroupIDs:                  []int64{7, 9},
		Platforms:                 []string{"anthropic", "openai"},
		CacheAverageWindowMinutes: 30 * 24 * 60,
	}
	cfg := v3BoardConfig(scope)

	// Group metadata as the V3 repository returns it: a name and a platform, nothing
	// else. There is no allow-list or pricing field in this shape at all.
	groupInfo := map[int64]channelMonitorV2GroupInfo{
		7: {name: "claude-main", platform: "anthropic"},
		9: {name: "gpt-main", platform: "openai"},
	}
	filter := service.ChannelMonitorV2Filter{
		Range:  "90m",
		Start:  time.Now().Add(-90 * time.Minute),
		End:    time.Now(),
		Bucket: 5 * time.Minute,
	}

	accs := seedChannelMonitorV2MatrixAccumulators(filter, cfg, service.ChannelMonitorV2GroupByPlatformGroup, groupInfo)

	require.Contains(t, accs, channelMonitorV2MatrixKey{platform: "anthropic", groupID: 7},
		"a group with no allow-list must still seed a board row")
	require.Contains(t, accs, channelMonitorV2MatrixKey{platform: "openai", groupID: 9},
		"a group with no pricing card must still seed a board row")
}

// The display model must stay the real model name: an empty allow-list means "show
// every model", not "collapse everything into __other__".
func TestChannelMonitorV3DoesNotCollapseModelsIntoOther(t *testing.T) {
	cfg := v3BoardConfig(service.ChannelMonitorV3BoardScope{
		GroupIDs:  []int64{7},
		Platforms: []string{"anthropic"},
	})

	require.Equal(t, "claude-sonnet-4",
		channelMonitorV2DisplayModel(cfg, "anthropic", "claude-sonnet-4"),
		"V3 must surface the real model name")
	require.NotEqual(t, service.ChannelMonitorV2OtherModel,
		channelMonitorV2DisplayModel(cfg, "anthropic", "claude-opus-4"))
}

// The derived config's platforms are only the ones V3 selected, so the board cannot
// accidentally pull in traffic from unrelated platforms.
func TestChannelMonitorV3BoardConfigLimitsPlatformsToSelection(t *testing.T) {
	cfg := v3BoardConfig(service.ChannelMonitorV3BoardScope{
		GroupIDs:  []int64{7},
		Platforms: []string{"anthropic"},
	})

	enabled := channelMonitorV2EnabledPlatforms(cfg)
	require.Equal(t, []string{"anthropic"}, enabled)
	require.NotContains(t, enabled, "openai")
}
