ALTER TABLE customer_cache
  ADD COLUMN last_order_at   TIMESTAMPTZ,
  ADD COLUMN order_addresses JSONB,
  ADD COLUMN order_names     TEXT[];

ALTER TABLE merchant_settings
  ADD COLUMN enable_behavioral_signals BOOLEAN NOT NULL DEFAULT false;
