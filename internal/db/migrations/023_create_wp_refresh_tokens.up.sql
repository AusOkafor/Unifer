CREATE TABLE IF NOT EXISTS wp_refresh_tokens (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    token_hash  TEXT        NOT NULL,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked     BOOLEAN     NOT NULL DEFAULT FALSE,
    UNIQUE (merchant_id, token_hash)
);

CREATE INDEX IF NOT EXISTS idx_wp_refresh_tokens_merchant
    ON wp_refresh_tokens (merchant_id)
    WHERE revoked = FALSE;
