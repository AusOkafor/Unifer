package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

type SnapshotRepository interface {
	Create(ctx context.Context, s *models.Snapshot) error
	FindByID(ctx context.Context, id uuid.UUID) (*models.Snapshot, error)
	FindByMergeRecord(ctx context.Context, mergeRecordID uuid.UUID) (*models.Snapshot, error)
	PurgeOlderThan(ctx context.Context, merchantID uuid.UUID, days int) (int64, error)
}

type snapshotRepo struct {
	db *sqlx.DB
}

func NewSnapshotRepo(db *sqlx.DB) SnapshotRepository {
	return &snapshotRepo{db: db}
}

func (r *snapshotRepo) Create(ctx context.Context, s *models.Snapshot) error {
	query := `
		INSERT INTO snapshots (merchant_id, merge_record_id, data)
		VALUES (:merchant_id, :merge_record_id, :data)
		RETURNING id, created_at`
	rows, err := r.db.NamedQueryContext(ctx, query, s)
	if err != nil {
		return fmt.Errorf("snapshot create: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return rows.Scan(&s.ID, &s.CreatedAt)
	}
	return nil
}

func (r *snapshotRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Snapshot, error) {
	var s models.Snapshot
	err := r.db.GetContext(ctx, &s, `SELECT * FROM snapshots WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("snapshot find: %w", err)
	}
	return &s, nil
}

func (r *snapshotRepo) FindByMergeRecord(ctx context.Context, mergeRecordID uuid.UUID) (*models.Snapshot, error) {
	var s models.Snapshot
	err := r.db.GetContext(ctx, &s,
		`SELECT * FROM snapshots WHERE merge_record_id = $1 ORDER BY created_at DESC LIMIT 1`,
		mergeRecordID,
	)
	if err != nil {
		return nil, fmt.Errorf("snapshot find by merge record: %w", err)
	}
	return &s, nil
}

func (r *snapshotRepo) PurgeOlderThan(ctx context.Context, merchantID uuid.UUID, days int) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM snapshots WHERE merchant_id = $1 AND created_at < $2`,
		merchantID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("snapshot purge: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
