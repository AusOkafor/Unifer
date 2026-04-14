DROP INDEX IF EXISTS idx_duplicate_groups_dismissed_at;
ALTER TABLE duplicate_groups
  DROP COLUMN IF EXISTS dismissed_at,
  DROP COLUMN IF EXISTS dismiss_reason,
  DROP COLUMN IF EXISTS confirmed_by_user;
