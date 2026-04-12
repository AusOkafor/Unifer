package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/services/identity"
	mergesvc "merger/backend/internal/services/merge"
	snapshotsvc "merger/backend/internal/services/snapshot"
)

// DetectPayload is the job payload for detect_duplicates jobs.
type DetectPayload struct {
	MerchantID string `json:"merchant_id"`
}

// MergePayload is the job payload for merge_customers jobs.
type MergePayload struct {
	MerchantID        string  `json:"merchant_id"`
	GroupID           string  `json:"group_id"`
	PrimaryCustomerID int64   `json:"primary_customer_id"`
	SecondaryIDs      []int64 `json:"secondary_ids"`
	PerformedBy       string  `json:"performed_by"`
}

// RestorePayload is the job payload for restore_snapshot jobs.
type RestorePayload struct {
	SnapshotID string `json:"snapshot_id"`
	MerchantID string `json:"merchant_id"`
}

// Processor handles execution of each job type.
type Processor struct {
	detector     *identity.Detector
	orchestrator *mergesvc.Orchestrator
	snapshotSvc  *snapshotsvc.Service
	jobRepo      repository.JobRepository
	log          zerolog.Logger
}

func NewProcessor(
	detector *identity.Detector,
	orchestrator *mergesvc.Orchestrator,
	snapshotSvc *snapshotsvc.Service,
	jobRepo repository.JobRepository,
	log zerolog.Logger,
) *Processor {
	return &Processor{
		detector:     detector,
		orchestrator: orchestrator,
		snapshotSvc:  snapshotSvc,
		jobRepo:      jobRepo,
		log:          log,
	}
}

// Process dispatches the job to the appropriate handler based on type.
func (p *Processor) Process(ctx context.Context, job *models.Job) error {
	switch job.Type {
	case models.JobTypeDetectDuplicates:
		return p.processDetect(ctx, job)
	case models.JobTypeMergeCustomers:
		return p.processMerge(ctx, job)
	case models.JobTypeRestoreSnapshot:
		return p.processRestore(ctx, job)
	default:
		return fmt.Errorf("unknown job type: %s", job.Type)
	}
}

func (p *Processor) processDetect(ctx context.Context, job *models.Job) error {
	var payload DetectPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal detect payload: %w", err)
	}
	merchantID, err := uuid.Parse(payload.MerchantID)
	if err != nil {
		return fmt.Errorf("invalid merchant id: %w", err)
	}
	return p.detector.RunDetection(ctx, merchantID)
}

func (p *Processor) processMerge(ctx context.Context, job *models.Job) error {
	var payload MergePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal merge payload: %w", err)
	}

	merchantID, err := uuid.Parse(payload.MerchantID)
	if err != nil {
		return fmt.Errorf("invalid merchant id: %w", err)
	}

	groupID := uuid.Nil
	if payload.GroupID != "" {
		groupID, _ = uuid.Parse(payload.GroupID)
	}

	req := mergesvc.MergeRequest{
		MerchantID:        merchantID,
		GroupID:           groupID,
		PrimaryCustomerID: payload.PrimaryCustomerID,
		SecondaryIDs:      payload.SecondaryIDs,
		PerformedBy:       payload.PerformedBy,
	}

	return p.orchestrator.Execute(ctx, req)
}

func (p *Processor) processRestore(ctx context.Context, job *models.Job) error {
	var payload RestorePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal restore payload: %w", err)
	}

	snapshotID, err := uuid.Parse(payload.SnapshotID)
	if err != nil {
		return fmt.Errorf("invalid snapshot id: %w", err)
	}

	snap, data, err := p.snapshotSvc.Get(ctx, snapshotID)
	if err != nil {
		return fmt.Errorf("get snapshot: %w", err)
	}

	// V1 restore: log the snapshot data for reference.
	// True restoration is a manual process since customerMerge is irreversible.
	p.log.Info().
		Str("snapshot_id", snap.ID.String()).
		Int("customer_count", len(data.Customers)).
		Msg("snapshot restore requested — snapshot data available for manual reconstruction")

	return nil
}
