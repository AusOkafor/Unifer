ALTER TABLE merchant_settings
  DROP COLUMN IF EXISTS enable_behavioral_signals;

ALTER TABLE customer_cache
  DROP COLUMN IF EXISTS last_order_at,
  DROP COLUMN IF EXISTS order_addresses,
  DROP COLUMN IF EXISTS order_names;
