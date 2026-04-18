ALTER TABLE merchant_settings
  DROP COLUMN IF EXISTS plan,
  DROP COLUMN IF EXISTS shopify_subscription_id,
  DROP COLUMN IF EXISTS merges_this_month,
  DROP COLUMN IF EXISTS merges_month_start;
