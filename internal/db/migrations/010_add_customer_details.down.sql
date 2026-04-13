ALTER TABLE customer_cache
  DROP COLUMN IF EXISTS note,
  DROP COLUMN IF EXISTS state,
  DROP COLUMN IF EXISTS verified_email,
  DROP COLUMN IF EXISTS shopify_created_at;
