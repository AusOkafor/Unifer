DROP INDEX IF EXISTS idx_merchants_platform;
ALTER TABLE merchants DROP COLUMN IF EXISTS platform;
