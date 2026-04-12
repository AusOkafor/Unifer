CREATE TABLE IF NOT EXISTS customer_cache (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id          UUID NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    shopify_customer_id  BIGINT NOT NULL,
    email                TEXT,
    name                 TEXT,
    phone                TEXT,
    address_json         JSONB,
    tags                 TEXT[] NOT NULL DEFAULT '{}',
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (merchant_id, shopify_customer_id)
);

CREATE INDEX IF NOT EXISTS idx_customer_cache_merchant_email
    ON customer_cache (merchant_id, email);

CREATE INDEX IF NOT EXISTS idx_customer_cache_merchant_shopify_id
    ON customer_cache (merchant_id, shopify_customer_id);
