ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS platform TEXT NOT NULL DEFAULT 'shopify';

CREATE INDEX IF NOT EXISTS idx_merchants_platform ON merchants (platform);
