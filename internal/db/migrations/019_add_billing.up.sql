-- Billing plan tracking on merchant_settings.
-- plan: 'free' | 'basic' | 'pro'
-- shopify_subscription_id: GID returned by appSubscriptionCreate
-- merges_this_month: rolling counter reset on the 1st of each month
-- merges_month_start: the date the current monthly window started

ALTER TABLE merchant_settings
  ADD COLUMN IF NOT EXISTS plan                  TEXT        NOT NULL DEFAULT 'free',
  ADD COLUMN IF NOT EXISTS shopify_subscription_id TEXT,
  ADD COLUMN IF NOT EXISTS merges_this_month     INT         NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS merges_month_start    DATE        NOT NULL DEFAULT CURRENT_DATE;
