-- Extend customer_cache with behavioral counters populated from Shopify data.
-- Used by the intelligence layer to recommend primary customers and score readiness.
ALTER TABLE customer_cache
  ADD COLUMN IF NOT EXISTS orders_count INT  NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS total_spent  TEXT NOT NULL DEFAULT '0.00';

-- Extend duplicate_groups with pre-merge intelligence.
-- readiness_score: 0–100 safety score computed from cached data.
-- intelligence_json: full IntelligenceReport blob (recommendation, risk flags, simulation).
ALTER TABLE duplicate_groups
  ADD COLUMN IF NOT EXISTS readiness_score   FLOAT,
  ADD COLUMN IF NOT EXISTS intelligence_json JSONB;
