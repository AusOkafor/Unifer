package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

// ConfidenceSourceCounts holds aggregate merge counts broken down by confidence source.
type ConfidenceSourceCounts struct {
	Behavioral int `db:"behavioral" json:"behavioral"`
	Profile    int `db:"profile" json:"profile"`
	Mixed      int `db:"mixed" json:"mixed"`
}

type MergeRepository interface {
	Create(ctx context.Context, r *models.MergeRecord) error
	ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit, offset int) ([]models.MergeRecord, int, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.MergeRecord, error)
	CountByConfidenceSource(ctx context.Context, merchantID uuid.UUID) (*ConfidenceSourceCounts, error)
}

type mergeRepo struct {
	db *sqlx.DB
}

func NewMergeRepo(db *sqlx.DB) MergeRepository {
	return &mergeRepo{db: db}
}

func (r *mergeRepo) Create(ctx context.Context, rec *models.MergeRecord) error {
	query := `
		INSERT INTO merge_records
			(merchant_id, primary_customer_id, secondary_customer_ids, orders_moved, performed_by, snapshot_id, confidence_source)
		VALUES
			(:merchant_id, :primary_customer_id, :secondary_customer_ids, :orders_moved, :performed_by, :snapshot_id, :confidence_source)
		RETURNING id, created_at`
	rows, err := r.db.NamedQueryContext(ctx, query, rec)
	if err != nil {
		return fmt.Errorf("merge record create: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return rows.Scan(&rec.ID, &rec.CreatedAt)
	}
	return nil
}

func (r *mergeRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit, offset int) ([]models.MergeRecord, int, error) {
	var records []models.MergeRecord
	var total int

	if err := r.db.GetContext(ctx, &total,
		`SELECT COUNT(*) FROM merge_records WHERE merchant_id = $1`, merchantID,
	); err != nil {
		return nil, 0, fmt.Errorf("merge record count: %w", err)
	}

	if err := r.db.SelectContext(ctx, &records,
		`SELECT * FROM merge_records WHERE merchant_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		merchantID, limit, offset,
	); err != nil {
		return nil, 0, fmt.Errorf("merge record list: %w", err)
	}

	return records, total, nil
}

func (r *mergeRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.MergeRecord, error) {
	var rec models.MergeRecord
	err := r.db.GetContext(ctx, &rec, `SELECT * FROM merge_records WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("merge record find: %w", err)
	}
	return &rec, nil
}

func (r *mergeRepo) CountByConfidenceSource(ctx context.Context, merchantID uuid.UUID) (*ConfidenceSourceCounts, error) {
	var counts ConfidenceSourceCounts
	err := r.db.GetContext(ctx, &counts, `
		SELECT
			COUNT(*) FILTER (WHERE confidence_source = 'behavioral') AS behavioral,
			COUNT(*) FILTER (WHERE confidence_source = 'profile')    AS profile,
			COUNT(*) FILTER (WHERE confidence_source = 'mixed')      AS mixed
		FROM merge_records
		WHERE merchant_id = $1`,
		merchantID,
	)
	if err != nil {
		return nil, fmt.Errorf("merge count by confidence source: %w", err)
	}
	return &counts, nil
}
