ALTER TABLE duplicate_groups
  ADD COLUMN IF NOT EXISTS risk_level TEXT
    CHECK (risk_level IN ('safe', 'review', 'risky'));

-- Backfill existing rows using confidence_score thresholds
UPDATE duplicate_groups SET risk_level =
  CASE
    WHEN confidence_score >= 0.90 THEN 'safe'
    WHEN confidence_score >= 0.75 THEN 'review'
    ELSE 'risky'
  END
WHERE risk_level IS NULL;

CREATE INDEX IF NOT EXISTS idx_duplicate_groups_risk_level
  ON duplicate_groups (merchant_id, risk_level, status);
