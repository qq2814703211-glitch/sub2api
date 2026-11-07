-- Channel pricing is maintained by the SQL repositories, not Ent schemas.
-- SQL NULL keeps all existing scalar pricing behavior unchanged.
ALTER TABLE channel_model_pricing
    ADD COLUMN IF NOT EXISTS media_pricing JSONB;
ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS media_pricing JSONB;

COMMENT ON COLUMN channel_model_pricing.media_pricing IS
    'Explicit media dimension pricing rules in site settlement units; NULL preserves legacy pricing';
COMMENT ON COLUMN channel_account_stats_model_pricing.media_pricing IS
    'Account statistics media dimension pricing rules in site settlement units; NULL preserves legacy pricing';
