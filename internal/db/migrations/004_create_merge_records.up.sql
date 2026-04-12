CREATE TABLE IF NOT EXISTS merge_records (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id           UUID NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    primary_customer_id   BIGINT NOT NULL,
    secondary_customer_ids BIGINT[] NOT NULL,
    orders_moved          INT NOT NULL DEFAULT 0,
    performed_by          TEXT NOT NULL DEFAULT '',
    snapshot_id           UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_merge_records_merchant
    ON merge_records (merchant_id, created_at DESC);
