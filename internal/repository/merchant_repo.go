package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

type MerchantRepository interface {
	Create(ctx context.Context, m *models.Merchant) error
	FindByDomain(ctx context.Context, domain string) (*models.Merchant, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.Merchant, error)
	UpdateToken(ctx context.Context, id uuid.UUID, encryptedToken string) error
}

type merchantRepo struct {
	db *sqlx.DB
}

func NewMerchantRepo(db *sqlx.DB) MerchantRepository {
	return &merchantRepo{db: db}
}

func (r *merchantRepo) Create(ctx context.Context, m *models.Merchant) error {
	query := `
		INSERT INTO merchants (shop_domain, access_token_enc)
		VALUES (:shop_domain, :access_token_enc)
		ON CONFLICT (shop_domain) DO UPDATE
		  SET access_token_enc = EXCLUDED.access_token_enc
		RETURNING id, created_at`
	rows, err := r.db.NamedQueryContext(ctx, query, m)
	if err != nil {
		return fmt.Errorf("merchant create: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return rows.Scan(&m.ID, &m.CreatedAt)
	}
	return nil
}

func (r *merchantRepo) FindByDomain(ctx context.Context, domain string) (*models.Merchant, error) {
	var m models.Merchant
	err := r.db.GetContext(ctx, &m, `SELECT * FROM merchants WHERE shop_domain = $1`, domain)
	if err != nil {
		return nil, fmt.Errorf("merchant find by domain: %w", err)
	}
	return &m, nil
}

func (r *merchantRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Merchant, error) {
	var m models.Merchant
	err := r.db.GetContext(ctx, &m, `SELECT * FROM merchants WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("merchant find by id: %w", err)
	}
	return &m, nil
}

func (r *merchantRepo) UpdateToken(ctx context.Context, id uuid.UUID, encryptedToken string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE merchants SET access_token_enc = $1 WHERE id = $2`,
		encryptedToken, id,
	)
	if err != nil {
		return fmt.Errorf("merchant update token: %w", err)
	}
	return nil
}
