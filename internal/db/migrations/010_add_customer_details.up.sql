ALTER TABLE customer_cache
  ADD COLUMN IF NOT EXISTS note               TEXT         NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS state              TEXT         NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS verified_email     BOOLEAN      NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS shopify_created_at TIMESTAMPTZ;
