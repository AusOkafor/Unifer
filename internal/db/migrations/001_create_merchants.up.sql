CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS merchants (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_domain      TEXT NOT NULL UNIQUE,
    access_token_enc TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
