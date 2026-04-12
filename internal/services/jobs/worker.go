package jobs

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/queue"
	"merger/backend/internal/repository"
)

const (
	popTimeout  = 5 * time.Second
	maxRetries  = 3
	recoverySweepInterval = 5 * time.Minute
	stuckJobAge = 10 * time.Minute
)

// Worker pulls jobs from the queue and processes them.
type Worker struct {
	q           *queue.Queue
	processor   *Processor
	jobRepo     repository.JobRepository
	concurrency int
	log         zerolog.Logger
}

func NewWorker(
	q *queue.Queue,
	processor *Processor,
	jobRepo repository.JobRepository,
	concurrency int,
	log zerolog.Logger,
) *Worker {
	if concurrency <= 0 {
		concurrency = 3
	}
	return &Worker{
		q:           q,
		processor:   processor,
		jobRepo:     jobRepo,
		concurrency: concurrency,
		log:         log,
	}
}

// Start launches worker goroutines and the recovery sweep. Blocks until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	var wg sync.WaitGroup

	// Worker goroutines
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			w.runLoop(ctx, workerID)
		}(i)
	}

	// Recovery sweep goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runRecoverySweep(ctx)
	}()

	wg.Wait()
	w.log.Info().Msg("all workers stopped")
}

func (w *Worker) runLoop(ctx context.Context, workerID int) {
	log := w.log.With().Int("worker", workerID).Logger()
	log.Info().Msg("worker started")

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("worker shutting down")
			return
		default:
		}

		jobIDStr, err := w.q.Pop(ctx, JobQueueName, popTimeout)
		if err != nil {
			log.Warn().Err(err).Msg("queue pop error")
			continue
		}
		if jobIDStr == "" {
			continue // timeout — no job available
		}

		w.processJob(ctx, log, jobIDStr)
	}
}

func (w *Worker) processJob(ctx context.Context, log zerolog.Logger, jobIDStr string) {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		log.Error().Str("job_id", jobIDStr).Msg("invalid job id in queue")
		w.q.Acknowledge(ctx, JobQueueName, jobIDStr)
		return
	}

	job, err := w.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		log.Error().Err(err).Str("job_id", jobIDStr).Msg("job not found in DB")
		w.q.Acknowledge(ctx, JobQueueName, jobIDStr)
		return
	}

	if job.Status == models.JobStatusCompleted || job.Status == models.JobStatusFailed {
		// Already processed (e.g., from recovery re-queue)
		w.q.Acknowledge(ctx, JobQueueName, jobIDStr)
		return
	}

	log.Info().Str("job_id", jobIDStr).Str("type", job.Type).Msg("processing job")
	w.jobRepo.UpdateStatus(ctx, jobID, models.JobStatusProcessing, nil)

	if err := w.processor.Process(ctx, job); err != nil {
		log.Error().Err(err).Str("job_id", jobIDStr).Msg("job processing failed")
		w.jobRepo.IncrementRetries(ctx, jobID)

		if job.Retries+1 >= maxRetries {
			w.jobRepo.UpdateStatus(ctx, jobID, models.JobStatusFailed, map[string]string{"error": err.Error()})
			log.Error().Str("job_id", jobIDStr).Int("retries", maxRetries).Msg("job permanently failed")
		} else {
			w.jobRepo.UpdateStatus(ctx, jobID, models.JobStatusQueued, nil)
			// Re-push to queue for retry
			w.q.Push(ctx, JobQueueName, jobIDStr)
		}
	} else {
		w.jobRepo.UpdateStatus(ctx, jobID, models.JobStatusCompleted, nil)
		log.Info().Str("job_id", jobIDStr).Msg("job completed")
	}

	w.q.Acknowledge(ctx, JobQueueName, jobIDStr)
}

// runRecoverySweep re-queues jobs stuck in processing state for too long.
// This handles crash recovery where a worker died mid-job.
func (w *Worker) runRecoverySweep(ctx context.Context) {
	ticker := time.NewTicker(recoverySweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stuckJobs, err := w.jobRepo.ListStuckJobs(ctx, stuckJobAge)
			if err != nil {
				w.log.Warn().Err(err).Msg("recovery sweep: list stuck jobs")
				continue
			}
			for _, job := range stuckJobs {
				w.log.Warn().Str("job_id", job.ID.String()).Msg("recovery sweep: re-queuing stuck job")
				w.jobRepo.UpdateStatus(ctx, job.ID, models.JobStatusQueued, nil)
				w.q.Push(ctx, JobQueueName, job.ID.String())
			}
		}
	}
}
