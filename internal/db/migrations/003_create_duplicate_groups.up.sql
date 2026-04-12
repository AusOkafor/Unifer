CREATE TABLE IF NOT EXISTS duplicate_groups (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    group_hash       TEXT NOT NULL,
    customer_ids     BIGINT[] NOT NULL,
    confidence_score FLOAT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'reviewed', 'merged')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_duplicate_groups_merchant_status
    ON duplicate_groups (merchant_id, status);

CREATE UNIQUE INDEX IF NOT EXISTS idx_duplicate_groups_merchant_hash
    ON duplicate_groups (merchant_id, group_hash)
    WHERE status != 'merged';
