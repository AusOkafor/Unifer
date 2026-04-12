CREATE TABLE IF NOT EXISTS merchant_settings (
    merchant_id           UUID PRIMARY KEY REFERENCES merchants(id) ON DELETE CASCADE,
    auto_detect           BOOLEAN NOT NULL DEFAULT TRUE,
    confidence_threshold  INT NOT NULL DEFAULT 75 CHECK (confidence_threshold BETWEEN 0 AND 100),
    retention_days        INT NOT NULL DEFAULT 90,
    notifications_enabled BOOLEAN NOT NULL DEFAULT TRUE
);
