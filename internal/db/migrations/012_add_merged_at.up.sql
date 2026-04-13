-- Records when a duplicate group was confirmed as a real merge.
-- This timestamp is a learning signal: we know that at this confidence + breakdown,
-- a human (or safe-bulk) confirmed the accounts were the same person.
ALTER TABLE duplicate_groups
  ADD COLUMN IF NOT EXISTS merged_at TIMESTAMPTZ;

-- Backfill existing merged rows with their created_at as a proxy timestamp.
UPDATE duplicate_groups
  SET merged_at = created_at
  WHERE status = 'merged' AND merged_at IS NULL;
