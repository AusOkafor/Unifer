ALTER TABLE customer_cache
    ADD COLUMN IF NOT EXISTS platform TEXT NOT NULL DEFAULT 'shopify';

ALTER TABLE customer_cache
    DROP CONSTRAINT IF EXISTS customer_cache_merchant_id_shopify_customer_id_key;

ALTER TABLE customer_cache
    ADD CONSTRAINT customer_cache_merchant_platform_ext_id_key
    UNIQUE (merchant_id, platform, shopify_customer_id);

CREATE INDEX IF NOT EXISTS idx_customer_cache_merchant_platform
    ON customer_cache (merchant_id, platform);
