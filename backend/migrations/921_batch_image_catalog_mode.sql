ALTER TABLE batch_image_jobs ADD COLUMN IF NOT EXISTS catalog_unit_mode TEXT NOT NULL DEFAULT '';
