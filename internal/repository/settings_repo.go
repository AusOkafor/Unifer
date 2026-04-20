package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

type SettingsRepository interface {
	Get(ctx context.Context, merchantID uuid.UUID) (*models.MerchantSettings, error)
	Upsert(ctx context.Context, s *models.MerchantSettings) error
	// UpdatePlan sets the billing plan + subscription ID.
	// Intentionally separate from Upsert so saving user settings never clobbers plan.
	UpdatePlan(ctx context.Context, merchantID uuid.UUID, plan string, subscriptionID *string) error
	// IncrementMergeCount bumps the monthly merge counter, resetting it if the
	// billing window has rolled over into a new month.
	IncrementMergeCount(ctx context.Context, merchantID uuid.UUID) error
}

type settingsRepo struct {
	db *sqlx.DB
}

func NewSettingsRepo(db *sqlx.DB) SettingsRepository {
	return &settingsRepo{db: db}
}

func (r *settingsRepo) Get(ctx context.Context, merchantID uuid.UUID) (*models.MerchantSettings, error) {
	var s models.MerchantSettings
	err := r.db.GetContext(ctx, &s,
		`SELECT * FROM merchant_settings WHERE merchant_id = $1`,
		merchantID,
	)
	if err == nil {
		return &s, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("settings get: %w", err)
	}

	// No settings row yet — create defaults and return them.
	defaults := models.DefaultSettings(merchantID)
	if upsertErr := r.Upsert(ctx, defaults); upsertErr != nil {
		return nil, fmt.Errorf("settings get: auto-create defaults: %w", upsertErr)
	}
	return defaults, nil
}

func (r *settingsRepo) Upsert(ctx context.Context, s *models.MerchantSettings) error {
	query := `
		INSERT INTO merchant_settings (
			merchant_id, auto_detect, confidence_threshold, retention_days,
			notifications_enabled,
			scan_frequency, scan_hour, signal_email, signal_phone, signal_address, signal_name,
			risk_policy, require_anchor, weak_link_protection,
			block_different_country, block_fraud_tags, block_disabled_accounts,
			bulk_max_batch, bulk_delay_ms, bulk_require_preview,
			notify_new_duplicates, notify_high_risk, notify_bulk_complete, notify_failures,
			debug_mode, enable_behavioral_signals
		) VALUES (
			:merchant_id, :auto_detect, :confidence_threshold, :retention_days,
			:notifications_enabled,
			:scan_frequency, :scan_hour, :signal_email, :signal_phone, :signal_address, :signal_name,
			:risk_policy, :require_anchor, :weak_link_protection,
			:block_different_country, :block_fraud_tags, :block_disabled_accounts,
			:bulk_max_batch, :bulk_delay_ms, :bulk_require_preview,
			:notify_new_duplicates, :notify_high_risk, :notify_bulk_complete, :notify_failures,
			:debug_mode, :enable_behavioral_signals
		)
		ON CONFLICT (merchant_id) DO UPDATE SET
			auto_detect            = EXCLUDED.auto_detect,
			confidence_threshold   = EXCLUDED.confidence_threshold,
			retention_days         = EXCLUDED.retention_days,
			notifications_enabled  = EXCLUDED.notifications_enabled,
			scan_frequency         = EXCLUDED.scan_frequency,
			scan_hour              = EXCLUDED.scan_hour,
			signal_email           = EXCLUDED.signal_email,
			signal_phone           = EXCLUDED.signal_phone,
			signal_address         = EXCLUDED.signal_address,
			signal_name            = EXCLUDED.signal_name,
			risk_policy            = EXCLUDED.risk_policy,
			require_anchor         = EXCLUDED.require_anchor,
			weak_link_protection   = EXCLUDED.weak_link_protection,
			block_different_country = EXCLUDED.block_different_country,
			block_fraud_tags       = EXCLUDED.block_fraud_tags,
			block_disabled_accounts = EXCLUDED.block_disabled_accounts,
			bulk_max_batch         = EXCLUDED.bulk_max_batch,
			bulk_delay_ms          = EXCLUDED.bulk_delay_ms,
			bulk_require_preview   = EXCLUDED.bulk_require_preview,
			notify_new_duplicates  = EXCLUDED.notify_new_duplicates,
			notify_high_risk       = EXCLUDED.notify_high_risk,
			notify_bulk_complete   = EXCLUDED.notify_bulk_complete,
			notify_failures        = EXCLUDED.notify_failures,
			debug_mode             = EXCLUDED.debug_mode,
			enable_behavioral_signals = EXCLUDED.enable_behavioral_signals`

	_, err := r.db.NamedExecContext(ctx, query, s)
	if err != nil {
		return fmt.Errorf("settings upsert: %w", err)
	}
	return nil
}

func (r *settingsRepo) UpdatePlan(ctx context.Context, merchantID uuid.UUID, plan string, subscriptionID *string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE merchant_settings
		    SET plan = $1, shopify_subscription_id = $2
		  WHERE merchant_id = $3`,
		plan, subscriptionID, merchantID,
	)
	if err != nil {
		return fmt.Errorf("settings update plan: %w", err)
	}
	return nil
}

func (r *settingsRepo) IncrementMergeCount(ctx context.Context, merchantID uuid.UUID) error {
	now := time.Now().UTC()
	// Reset the counter when we've rolled into a new calendar month.
	_, err := r.db.ExecContext(ctx,
		`UPDATE merchant_settings
		    SET merges_this_month = CASE
		          WHEN DATE_TRUNC('month', merges_month_start) < DATE_TRUNC('month', $2::timestamptz)
		          THEN 1
		          ELSE merges_this_month + 1
		        END,
		        merges_month_start = CASE
		          WHEN DATE_TRUNC('month', merges_month_start) < DATE_TRUNC('month', $2::timestamptz)
		          THEN $2::date
		          ELSE merges_month_start
		        END
		  WHERE merchant_id = $1`,
		merchantID, now,
	)
	if err != nil {
		return fmt.Errorf("settings increment merge count: %w", err)
	}
	return nil
}
