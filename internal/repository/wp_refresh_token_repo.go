package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

type WPRefreshTokenRepository interface {
	Create(ctx context.Context, t *models.WPRefreshToken) error
	FindValid(ctx context.Context, merchantID uuid.UUID, tokenHash string) (*models.WPRefreshToken, error)
	RevokeAll(ctx context.Context, merchantID uuid.UUID) error
}

type wpRefreshTokenRepo struct {
	db *sqlx.DB
}

func NewWPRefreshTokenRepo(db *sqlx.DB) WPRefreshTokenRepository {
	return &wpRefreshTokenRepo{db: db}
}

func (r *wpRefreshTokenRepo) Create(ctx context.Context, t *models.WPRefreshToken) error {
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO wp_refresh_tokens (merchant_id, token_hash, expires_at)
		VALUES (:merchant_id, :token_hash, :expires_at)`,
		t,
	)
	if err != nil {
		return fmt.Errorf("wp refresh token create: %w", err)
	}
	return nil
}

func (r *wpRefreshTokenRepo) FindValid(ctx context.Context, merchantID uuid.UUID, tokenHash string) (*models.WPRefreshToken, error) {
	var t models.WPRefreshToken
	err := r.db.GetContext(ctx, &t, `
		SELECT * FROM wp_refresh_tokens
		WHERE merchant_id = $1
		  AND token_hash  = $2
		  AND revoked     = FALSE
		  AND expires_at  > NOW()`,
		merchantID, tokenHash,
	)
	if err != nil {
		return nil, fmt.Errorf("wp refresh token find: %w", err)
	}
	return &t, nil
}

func (r *wpRefreshTokenRepo) RevokeAll(ctx context.Context, merchantID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE wp_refresh_tokens SET revoked = TRUE WHERE merchant_id = $1`,
		merchantID,
	)
	if err != nil {
		return fmt.Errorf("wp refresh token revoke all: %w", err)
	}
	return nil
}
