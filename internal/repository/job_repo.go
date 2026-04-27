package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

type JobRepository interface {
	Create(ctx context.Context, j *models.Job) error
	FindByID(ctx context.Context, id uuid.UUID) (*models.Job, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, result interface{}) error
	IncrementRetries(ctx context.Context, id uuid.UUID) error
	ListStuckJobs(ctx context.Context, maxAge time.Duration) ([]models.Job, error)
	CountPendingByType(ctx context.Context, merchantID uuid.UUID, jobType string) (int, error)
}

type jobRepo struct {
	db *sqlx.DB
}

func NewJobRepo(db *sqlx.DB) JobRepository {
	return &jobRepo{db: db}
}

func (r *jobRepo) Create(ctx context.Context, j *models.Job) error {
	query := `
		INSERT INTO jobs (merchant_id, type, status, payload)
		VALUES (:merchant_id, :type, :status, :payload)
		RETURNING id, created_at, updated_at`
	rows, err := r.db.NamedQueryContext(ctx, query, j)
	if err != nil {
		return fmt.Errorf("job create: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return rows.Scan(&j.ID, &j.CreatedAt, &j.UpdatedAt)
	}
	return nil
}

func (r *jobRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Job, error) {
	var j models.Job
	err := r.db.GetContext(ctx, &j, `SELECT * FROM jobs WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("job find: %w", err)
	}
	return &j, nil
}

func (r *jobRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, result interface{}) error {
	var resultJSON json.RawMessage
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal job result: %w", err)
		}
		resultJSON = b
	}

	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = $1, result = COALESCE($2, result), updated_at = NOW() WHERE id = $3`,
		status, resultJSON, id,
	)
	if err != nil {
		return fmt.Errorf("job update status: %w", err)
	}
	return nil
}

func (r *jobRepo) IncrementRetries(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET retries = retries + 1, updated_at = NOW() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("job increment retries: %w", err)
	}
	return nil
}

func (r *jobRepo) ListStuckJobs(ctx context.Context, maxAge time.Duration) ([]models.Job, error) {
	cutoff := time.Now().Add(-maxAge)
	var jobs []models.Job
	err := r.db.SelectContext(ctx, &jobs,
		`SELECT * FROM jobs WHERE status = 'processing' AND updated_at < $1`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("job list stuck: %w", err)
	}
	return jobs, nil
}

func (r *jobRepo) CountPendingByType(ctx context.Context, merchantID uuid.UUID, jobType string) (int, error) {
	var count int
	// Only debounce on 'queued' — not 'processing'. A job stuck in processing
	// (e.g., worker crashed) should not block new webhook-triggered detections.
	// The recovery sweep handles re-queuing stuck jobs separately.
	err := r.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM jobs WHERE merchant_id = $1 AND type = $2 AND status = 'queued'`,
		merchantID, jobType,
	)
	if err != nil {
		return 0, fmt.Errorf("job count pending: %w", err)
	}
	return count, nil
}
