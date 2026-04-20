ALTER TABLE merchant_settings
    ADD COLUMN IF NOT EXISTS scan_hour INTEGER NOT NULL DEFAULT 3
        CHECK (scan_hour >= 0 AND scan_hour <= 23);
