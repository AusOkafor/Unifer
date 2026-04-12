package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/queue"
	"merger/backend/internal/repository"
)

const JobQueueName = "jobs"

// Dispatcher creates job records in the DB and enqueues them for workers.
type Dispatcher struct {
	jobRepo  repository.JobRepository
	queue    *queue.Queue
	log      zerolog.Logger
}

func NewDispatcher(jobRepo repository.JobRepository, q *queue.Queue, log zerolog.Logger) *Dispatcher {
	return &Dispatcher{
		jobRepo: jobRepo,
		queue:   q,
		log:     log,
	}
}

// Dispatch creates a new job and pushes its ID to the Redis queue.
// For detect_duplicates, it skips dispatch if a job for this merchant is already pending.
func (d *Dispatcher) Dispatch(ctx context.Context, jobType string, merchantID uuid.UUID, payload interface{}) (uuid.UUID, error) {
	// Debounce: skip duplicate detect jobs
	if jobType == models.JobTypeDetectDuplicates {
		count, err := d.jobRepo.CountPendingByType(ctx, merchantID, jobType)
		if err == nil && count > 0 {
			d.log.Debug().Str("type", jobType).Str("merchant", merchantID.String()).
				Msg("detect job already pending — skipping dispatch")
			return uuid.Nil, nil
		}
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal job payload: %w", err)
	}

	job := &models.Job{
		MerchantID: merchantID,
		Type:       jobType,
		Status:     models.JobStatusQueued,
		Payload:    payloadJSON,
	}

	if err := d.jobRepo.Create(ctx, job); err != nil {
		return uuid.Nil, fmt.Errorf("create job record: %w", err)
	}

	if err := d.queue.Push(ctx, JobQueueName, job.ID.String()); err != nil {
		// Log but don't fail — the DB record exists, recovery sweep can re-queue
		d.log.Warn().Err(err).Str("job_id", job.ID.String()).Msg("failed to push job to queue")
	}

	d.log.Info().Str("job_id", job.ID.String()).Str("type", jobType).
		Str("merchant", merchantID.String()).Msg("job dispatched")

	return job.ID, nil
}
