package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

type SettingsRepository interface {
	Get(ctx context.Context, merchantID uuid.UUID) (*models.MerchantSettings, error)
	Upsert(ctx context.Context, s *models.MerchantSettings) error
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
	if err != nil {
		return nil, fmt.Errorf("settings get: %w", err)
	}
	return &s, nil
}

func (r *settingsRepo) Upsert(ctx context.Context, s *models.MerchantSettings) error {
	query := `
		INSERT INTO merchant_settings (
			merchant_id, auto_detect, confidence_threshold, retention_days,
			notifications_enabled,
			scan_frequency, signal_email, signal_phone, signal_address, signal_name,
			risk_policy, require_anchor, weak_link_protection,
			block_different_country, block_fraud_tags, block_disabled_accounts,
			bulk_max_batch, bulk_delay_ms, bulk_require_preview,
			notify_new_duplicates, notify_high_risk, notify_bulk_complete, notify_failures,
			debug_mode
		) VALUES (
			:merchant_id, :auto_detect, :confidence_threshold, :retention_days,
			:notifications_enabled,
			:scan_frequency, :signal_email, :signal_phone, :signal_address, :signal_name,
			:risk_policy, :require_anchor, :weak_link_protection,
			:block_different_country, :block_fraud_tags, :block_disabled_accounts,
			:bulk_max_batch, :bulk_delay_ms, :bulk_require_preview,
			:notify_new_duplicates, :notify_high_risk, :notify_bulk_complete, :notify_failures,
			:debug_mode
		)
		ON CONFLICT (merchant_id) DO UPDATE SET
			auto_detect            = EXCLUDED.auto_detect,
			confidence_threshold   = EXCLUDED.confidence_threshold,
			retention_days         = EXCLUDED.retention_days,
			notifications_enabled  = EXCLUDED.notifications_enabled,
			scan_frequency         = EXCLUDED.scan_frequency,
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
			debug_mode             = EXCLUDED.debug_mode`

	_, err := r.db.NamedExecContext(ctx, query, s)
	if err != nil {
		return fmt.Errorf("settings upsert: %w", err)
	}
	return nil
}
