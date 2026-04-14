-- Gap 7: negative feedback loop — track dismissed groups with reasons.
-- Gap 6: structured learning signal — track whether a human manually confirmed the merge.
ALTER TABLE duplicate_groups
  ADD COLUMN IF NOT EXISTS dismissed_at     TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS dismiss_reason   TEXT,
  ADD COLUMN IF NOT EXISTS confirmed_by_user BOOLEAN NOT NULL DEFAULT false;

-- Index for analytics: how often are groups dismissed vs merged?
CREATE INDEX IF NOT EXISTS idx_duplicate_groups_dismissed_at
  ON duplicate_groups (merchant_id, dismissed_at)
  WHERE dismissed_at IS NOT NULL;
