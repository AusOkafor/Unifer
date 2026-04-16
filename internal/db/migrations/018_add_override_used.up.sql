-- Track whether a merge was performed using the disabled-account override.
-- Recorded for audit trail and future analytics (override merge rate).
ALTER TABLE merge_records
  ADD COLUMN IF NOT EXISTS override_used BOOLEAN NOT NULL DEFAULT FALSE;
