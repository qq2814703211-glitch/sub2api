//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// The DSN must point to a disposable empty database: this runs real migrations.
// No production connection is discovered or used by the test.
func TestMediaPricingPostgresRoundTrip(t *testing.T) {
	dsn := os.Getenv("MEDIA_PRICING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated media pricing PostgreSQL DSN not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	var media service.MediaPricing
	require.NoError(t, json.Unmarshal([]byte(mediaPricingFixtureJSON), &media))
	price := service.ChannelModelPricing{Platform: service.PlatformOpenAI, Models: []string{"fixture"}, BillingMode: service.BillingModeImage, MediaPricing: &media}
	channels := &channelRepository{db: db}
	channel := &service.Channel{Name: t.Name(), Status: service.StatusActive, BillingModelSource: service.BillingModelSourceRequested, ModelPricing: []service.ChannelModelPricing{price}, AccountStatsPricingRules: []service.AccountStatsPricingRule{{Name: "fixture-stats", GroupIDs: []int64{}, AccountIDs: []int64{}, Pricing: []service.ChannelModelPricing{price}}}}
	require.NoError(t, channels.Create(ctx, channel))
	t.Cleanup(func() { require.NoError(t, channels.Delete(ctx, channel.ID)) })
	stored, err := channels.GetByID(ctx, channel.ID)
	require.NoError(t, err)
	require.Equal(t, &media, stored.ModelPricing[0].MediaPricing)
	require.Equal(t, &media, stored.AccountStatsPricingRules[0].Pricing[0].MediaPricing)
	for _, target := range []struct {
		table string
		id    int64
	}{{"channel_model_pricing", stored.ModelPricing[0].ID}, {"channel_account_stats_model_pricing", stored.AccountStatsPricingRules[0].Pricing[0].ID}} {
		var raw string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT media_pricing::text FROM "+target.table+" WHERE id=$1", target.id).Scan(&raw))
		require.JSONEq(t, mediaPricingFixtureJSON, raw)
	}
	batch, err := channels.batchLoadModelPricing(ctx, []int64{channel.ID})
	require.NoError(t, err)
	require.Equal(t, &media, batch[channel.ID][0].MediaPricing)
	statsBatch, err := channels.batchLoadAccountStatsPricingRules(ctx, []int64{channel.ID})
	require.NoError(t, err)
	require.Equal(t, &media, statsBatch[channel.ID][0].Pricing[0].MediaPricing)
	// Individual create/update/clear and transactional replacement all use the column.
	standalone := price.Clone()
	standalone.ChannelID = channel.ID
	require.NoError(t, channels.CreateModelPricing(ctx, &standalone))
	standalone.MediaPricing = nil
	require.NoError(t, channels.UpdateModelPricing(ctx, &standalone))
	var cleared sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, "SELECT media_pricing::text FROM channel_model_pricing WHERE id=$1", standalone.ID).Scan(&cleared))
	require.False(t, cleared.Valid)
	require.NoError(t, channels.ReplaceModelPricing(ctx, channel.ID, []service.ChannelModelPricing{price}))
	invalid := price.Clone()
	invalid.MediaPricing.Rules[0].Price = nil
	require.Error(t, channels.ReplaceModelPricing(ctx, channel.ID, []service.ChannelModelPricing{invalid}))
	stored, err = channels.GetByID(ctx, channel.ID)
	require.NoError(t, err)
	require.Equal(t, &media, stored.ModelPricing[0].MediaPricing, "failed replacement rolls back deletion")
	stored.AccountStatsPricingRules[0].Pricing[0].MediaPricing = nil
	require.NoError(t, channels.Update(ctx, stored))
	clearedStats, err := channels.GetByID(ctx, channel.ID)
	require.NoError(t, err)
	require.Nil(t, clearedStats.AccountStatsPricingRules[0].Pricing[0].MediaPricing)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT media_pricing::text FROM channel_account_stats_model_pricing WHERE id=$1", clearedStats.AccountStatsPricingRules[0].Pricing[0].ID).Scan(&cleared))
	require.False(t, cleared.Valid)

	// Group retains the existing Ent JSON schema, including explicit zero/false.
	groups := newGroupRepositoryWithSQL(client, db)
	group := &service.Group{Name: t.Name() + "-group", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 2, ModelPricing: []service.ChannelModelPricing{price}}
	require.NoError(t, groups.Create(ctx, group))
	t.Cleanup(func() { require.NoError(t, groups.Delete(ctx, group.ID)) })
	loaded, err := groups.GetByID(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, &media, loaded.ModelPricing[0].MediaPricing)
	var rawGroup string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT model_pricing->0->'media_pricing' FROM groups WHERE id=$1", group.ID).Scan(&rawGroup))
	require.JSONEq(t, mediaPricingFixtureJSON, rawGroup)
	entity, err := client.Group.Get(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, &media, groupEntityToService(entity).ModelPricing[0].MediaPricing, "auth mapper preserves rules")
	billing := service.NewBillingService(&config.Config{}, nil)
	resolver := service.NewModelPricingResolver(nil, billing)
	audio, ref, layer := false, true, false
	inputImages := 0 // exact synthetic text-only input, not unknown
	cost, err := billing.CalculateCostUnified(service.CostInput{Ctx: ctx, Model: "fixture", Group: loaded, Resolver: resolver, RateMultiplier: loaded.RateMultiplier, MediaUsage: &service.MediaUsage{InputImageCount: &inputImages, Count: 2, Resolution: "2K", Mode: "standard", InputType: "text", Audio: &audio, ReferenceVideo: &ref, Layer: &layer, Pixels: 4194304, DurationSeconds: 5, FPS: 24}})
	require.NoError(t, err)
	require.InDelta(t, 0.03, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.06, cost.ActualCost, 1e-12)
	loaded.ModelPricing[0].MediaPricing = nil
	require.NoError(t, groups.Update(ctx, loaded))
	reloaded, err := groups.GetByID(ctx, group.ID)
	require.NoError(t, err)
	require.Nil(t, reloaded.ModelPricing[0].MediaPricing)
}
