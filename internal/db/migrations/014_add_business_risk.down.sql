DROP INDEX IF EXISTS idx_duplicate_groups_business_risk;
ALTER TABLE duplicate_groups
    DROP COLUMN IF EXISTS business_risk_level,
    DROP COLUMN IF EXISTS impact_score;
