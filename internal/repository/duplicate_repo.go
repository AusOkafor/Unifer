package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

type DuplicateRepository interface {
	CreateGroup(ctx context.Context, g *models.DuplicateGroup) error
	DeletePendingByMerchant(ctx context.Context, merchantID uuid.UUID) (int64, error)
	ListByMerchant(ctx context.Context, merchantID uuid.UUID, status string, minConfidence float64, limit, offset int) ([]models.DuplicateGroup, int, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.DuplicateGroup, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) error
}

type duplicateRepo struct {
	db *sqlx.DB
}

func NewDuplicateRepo(db *sqlx.DB) DuplicateRepository {
	return &duplicateRepo{db: db}
}

func (r *duplicateRepo) CreateGroup(ctx context.Context, g *models.DuplicateGroup) error {
	query := `
		INSERT INTO duplicate_groups
			(merchant_id, group_hash, customer_ids, confidence_score, status, readiness_score, intelligence_json)
		VALUES
			(:merchant_id, :group_hash, :customer_ids, :confidence_score, :status, :readiness_score, :intelligence_json)
		ON CONFLICT (merchant_id, group_hash) WHERE status != 'merged' DO UPDATE SET
			confidence_score  = EXCLUDED.confidence_score,
			customer_ids      = EXCLUDED.customer_ids,
			readiness_score   = EXCLUDED.readiness_score,
			intelligence_json = EXCLUDED.intelligence_json
		RETURNING id, created_at`
	rows, err := r.db.NamedQueryContext(ctx, query, g)
	if err != nil {
		return fmt.Errorf("duplicate group create: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return rows.Scan(&g.ID, &g.CreatedAt)
	}
	return nil
}

func (r *duplicateRepo) DeletePendingByMerchant(ctx context.Context, merchantID uuid.UUID) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM duplicate_groups WHERE merchant_id = $1 AND status = 'pending'`,
		merchantID,
	)
	if err != nil {
		return 0, fmt.Errorf("delete pending groups: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (r *duplicateRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, status string, minConfidence float64, limit, offset int) ([]models.DuplicateGroup, int, error) {
	var groups []models.DuplicateGroup
	var total int

	baseWhere := `merchant_id = $1`
	args := []interface{}{merchantID}
	argIdx := 2

	switch status {
	case "all":
		// No additional filter — return every status including merged.
	case "":
		// Default: exclude merged so the list only shows actionable items.
		baseWhere += ` AND status != 'merged'`
	default:
		baseWhere += fmt.Sprintf(` AND status = $%d`, argIdx)
		args = append(args, status)
		argIdx++
	}

	// Apply merchant's confidence threshold (0 = no filter).
	if minConfidence > 0 {
		baseWhere += fmt.Sprintf(` AND confidence_score >= $%d`, argIdx)
		args = append(args, minConfidence)
		argIdx++
	}

	err := r.db.GetContext(ctx, &total,
		`SELECT COUNT(*) FROM duplicate_groups WHERE `+baseWhere,
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("duplicate group count: %w", err)
	}

	listArgs := append(args, limit, offset)
	err = r.db.SelectContext(ctx, &groups,
		fmt.Sprintf(`SELECT * FROM duplicate_groups WHERE %s ORDER BY confidence_score DESC LIMIT $%d OFFSET $%d`,
			baseWhere, argIdx, argIdx+1),
		listArgs...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("duplicate group list: %w", err)
	}

	return groups, total, nil
}

func (r *duplicateRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.DuplicateGroup, error) {
	var g models.DuplicateGroup
	err := r.db.GetContext(ctx, &g, `SELECT * FROM duplicate_groups WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("duplicate group find: %w", err)
	}
	return &g, nil
}

func (r *duplicateRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE duplicate_groups SET status = $1 WHERE id = $2`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("duplicate group update status: %w", err)
	}
	return nil
}
