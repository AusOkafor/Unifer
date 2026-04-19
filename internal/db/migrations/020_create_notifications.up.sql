CREATE TABLE notifications (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    type        VARCHAR(50) NOT NULL,
    title       VARCHAR(255) NOT NULL,
    body        TEXT        NOT NULL DEFAULT '',
    is_read     BOOLEAN     NOT NULL DEFAULT false,
    action_url  VARCHAR(500) NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_merchant ON notifications(merchant_id, is_read, created_at DESC);
