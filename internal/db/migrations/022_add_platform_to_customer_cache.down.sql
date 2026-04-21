DROP INDEX IF EXISTS idx_customer_cache_merchant_platform;

ALTER TABLE customer_cache
    DROP CONSTRAINT IF EXISTS customer_cache_merchant_platform_ext_id_key;

ALTER TABLE customer_cache
    DROP COLUMN IF EXISTS platform;

ALTER TABLE customer_cache
    ADD CONSTRAINT customer_cache_merchant_id_shopify_customer_id_key
    UNIQUE (merchant_id, shopify_customer_id);
