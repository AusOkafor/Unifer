ALTER TABLE merchant_settings
  -- Detection
  ADD COLUMN IF NOT EXISTS scan_frequency          TEXT    NOT NULL DEFAULT 'webhook',
  ADD COLUMN IF NOT EXISTS signal_email            BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS signal_phone            BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS signal_address          BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS signal_name             BOOLEAN NOT NULL DEFAULT TRUE,
  -- Risk & Safety
  ADD COLUMN IF NOT EXISTS risk_policy             TEXT    NOT NULL DEFAULT 'safe_only',
  ADD COLUMN IF NOT EXISTS require_anchor          BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS weak_link_protection    BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS block_different_country BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS block_fraud_tags        BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS block_disabled_accounts BOOLEAN NOT NULL DEFAULT TRUE,
  -- Bulk Merge
  ADD COLUMN IF NOT EXISTS bulk_max_batch          INT     NOT NULL DEFAULT 25,
  ADD COLUMN IF NOT EXISTS bulk_delay_ms           INT     NOT NULL DEFAULT 500,
  ADD COLUMN IF NOT EXISTS bulk_require_preview    BOOLEAN NOT NULL DEFAULT TRUE,
  -- Granular notifications
  ADD COLUMN IF NOT EXISTS notify_new_duplicates   BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS notify_high_risk        BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS notify_bulk_complete    BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS notify_failures         BOOLEAN NOT NULL DEFAULT TRUE,
  -- Developer / debug
  ADD COLUMN IF NOT EXISTS debug_mode              BOOLEAN NOT NULL DEFAULT FALSE;
