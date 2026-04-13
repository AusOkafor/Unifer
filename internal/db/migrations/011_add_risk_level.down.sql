DROP INDEX IF EXISTS idx_duplicate_groups_risk_level;
ALTER TABLE duplicate_groups DROP COLUMN IF EXISTS risk_level;
