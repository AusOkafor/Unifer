-- Business risk classification and blast-radius score stored at detection time.
-- business_risk_level: "high" | "medium" | "low" — commercial stakes of the merge,
--   independent of identity confidence (are they the same person?).
-- impact_score: cluster_size × avg_customer_value — how much is at stake if wrong.
ALTER TABLE duplicate_groups
    ADD COLUMN IF NOT EXISTS business_risk_level TEXT,
    ADD COLUMN IF NOT EXISTS impact_score        DOUBLE PRECISION;

CREATE INDEX IF NOT EXISTS idx_duplicate_groups_business_risk
    ON duplicate_groups (merchant_id, business_risk_level)
    WHERE business_risk_level IS NOT NULL;
