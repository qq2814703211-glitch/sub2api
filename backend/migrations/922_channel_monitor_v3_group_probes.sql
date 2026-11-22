-- Keep each group's effective identity, including its ciphertext, without
-- decrypting it or mixing fields from two identities. Match HasOwnProbe.
UPDATE channel_monitor_v3_config AS config
SET groups = COALESCE((
        SELECT jsonb_agg((CASE
            WHEN btrim(COALESCE(entry->'probe'->>'endpoint', '')) <> ''
             AND btrim(COALESCE(entry->'probe'->>'primary_model', '')) <> '' THEN entry
            ELSE jsonb_set(entry, '{probe}', config.probe)
        END) - 'probe_inherited' ORDER BY position)
        FROM jsonb_array_elements(config.groups) WITH ORDINALITY AS items(entry, position)
    ), '[]'::jsonb),
    probe = '{}'::jsonb, version = version + 1, updated_at = NOW()
WHERE config.probe <> '{}'::jsonb;

-- An older instance must not reintroduce a shared credential after migration.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'channel_monitor_v3_config'::regclass
          AND conname = 'channel_monitor_v3_no_shared_probe'
    ) THEN
        ALTER TABLE channel_monitor_v3_config
            ADD CONSTRAINT channel_monitor_v3_no_shared_probe CHECK (probe = '{}'::jsonb);
    END IF;
END $$;
