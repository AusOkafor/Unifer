ALTER TABLE duplicate_groups
  DROP COLUMN IF EXISTS intelligence_json,
  DROP COLUMN IF EXISTS readiness_score;

ALTER TABLE customer_cache
  DROP COLUMN IF EXISTS total_spent,
  DROP COLUMN IF EXISTS orders_count;
