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
		INSERT INTO merchant_settings
			(merchant_id, auto_detect, confidence_threshold, retention_days, notifications_enabled)
		VALUES
			(:merchant_id, :auto_detect, :confidence_threshold, :retention_days, :notifications_enabled)
		ON CONFLICT (merchant_id) DO UPDATE SET
			auto_detect           = EXCLUDED.auto_detect,
			confidence_threshold  = EXCLUDED.confidence_threshold,
			retention_days        = EXCLUDED.retention_days,
			notifications_enabled = EXCLUDED.notifications_enabled`
	_, err := r.db.NamedExecContext(ctx, query, s)
	if err != nil {
		return fmt.Errorf("settings upsert: %w", err)
	}
	return nil
}
