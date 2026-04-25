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
	notifsvc "merger/backend/internal/services/notification"
	snapshotsvc "merger/backend/internal/services/snapshot"
	syncsvc "merger/backend/internal/services/sync"
)

// SyncPayload is the job payload for sync_customers jobs.
type SyncPayload struct {
	MerchantID string `json:"merchant_id"`
}

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
	// OverrideDisabled records that the user explicitly bypassed the
	// disabled_account hard block. Passed to the orchestrator for audit logging.
	OverrideDisabled bool   `json:"override_disabled"`
	// Plan is the merchant's billing plan at dispatch time, forwarded to the
	// orchestrator so it can gate plan-only operations (e.g. snapshot creation).
	Plan             string `json:"plan"`
}

// RestorePayload is the job payload for restore_snapshot jobs.
type RestorePayload struct {
	SnapshotID string `json:"snapshot_id"`
	MerchantID string `json:"merchant_id"`
}

// Processor handles execution of each job type.
type Processor struct {
	detector          *identity.Detector
	orchestrator      *mergesvc.Orchestrator
	wpOrchestrator    *mergesvc.Orchestrator // nil when WP is not wired
	snapshotSvc       *snapshotsvc.Service
	syncSvc           *syncsvc.Service
	jobRepo           repository.JobRepository
	customerCacheRepo repository.CustomerCacheRepository
	dispatcher        *Dispatcher
	notifSvc          *notifsvc.Service // may be nil — notifications skipped if not wired
	log               zerolog.Logger
}

func NewProcessor(
	detector *identity.Detector,
	orchestrator *mergesvc.Orchestrator,
	snapshotSvc *snapshotsvc.Service,
	syncSvc *syncsvc.Service,
	jobRepo repository.JobRepository,
	customerCacheRepo repository.CustomerCacheRepository,
	dispatcher *Dispatcher,
	notifSvc *notifsvc.Service,
	log zerolog.Logger,
) *Processor {
	return &Processor{
		detector:          detector,
		orchestrator:      orchestrator,
		snapshotSvc:       snapshotSvc,
		syncSvc:           syncSvc,
		jobRepo:           jobRepo,
		customerCacheRepo: customerCacheRepo,
		dispatcher:        dispatcher,
		notifSvc:          notifSvc,
		log:               log,
	}
}

// SetWPOrchestrator injects the WordPress-specific orchestrator.
// Call this after NewProcessor when WordPress support is enabled.
func (p *Processor) SetWPOrchestrator(o *mergesvc.Orchestrator) { p.wpOrchestrator = o }

// Process dispatches the job to the appropriate handler based on type.
func (p *Processor) Process(ctx context.Context, job *models.Job) error {
	switch job.Type {
	case models.JobTypeSyncCustomers:
		return p.processSync(ctx, job)
	case models.JobTypeDetectDuplicates:
		return p.processDetect(ctx, job)
	case models.JobTypeMergeCustomers:
		return p.processMerge(ctx, job)
	case models.JobTypeMergeCustomersWordPress:
		return p.processWPMerge(ctx, job)
	case models.JobTypeRestoreSnapshot:
		return p.processRestore(ctx, job)
	default:
		return fmt.Errorf("unknown job type: %s", job.Type)
	}
}

func (p *Processor) processSync(ctx context.Context, job *models.Job) error {
	var payload SyncPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal sync payload: %w", err)
	}
	merchantID, err := uuid.Parse(payload.MerchantID)
	if err != nil {
		return fmt.Errorf("invalid merchant id: %w", err)
	}

	count, err := p.syncSvc.SyncCustomers(ctx, merchantID)
	if err != nil {
		return fmt.Errorf("sync customers: %w", err)
	}
	p.log.Info().Int("count", count).Msg("sync: complete, queuing detection")

	// Auto-trigger duplicate detection after sync.
	if p.dispatcher != nil {
		if _, err := p.dispatcher.Dispatch(ctx, models.JobTypeDetectDuplicates, merchantID,
			map[string]string{"merchant_id": merchantID.String()}); err != nil {
			p.log.Warn().Err(err).Msg("sync: failed to queue detect job")
		}
	}
	return nil
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
	if err := p.detector.RunDetection(ctx, merchantID); err != nil {
		return err
	}
	// Notify after successful detection (non-blocking — best-effort).
	if p.notifSvc != nil {
		p.notifSvc.OnDetectComplete(ctx, merchantID)
	}
	return nil
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
		OverrideDisabled:  payload.OverrideDisabled,
		Plan:              payload.Plan,
	}

	if err := p.orchestrator.Execute(ctx, req); err != nil {
		if p.notifSvc != nil {
			p.notifSvc.OnMergeFailed(ctx, merchantID, err)
		}
		return err
	}
	if p.notifSvc != nil {
		p.notifSvc.OnMergeComplete(ctx, merchantID, payload.PrimaryCustomerID, len(payload.SecondaryIDs))
	}
	return nil
}

func (p *Processor) processWPMerge(ctx context.Context, job *models.Job) error {
	if p.wpOrchestrator == nil {
		return fmt.Errorf("wp merge: orchestrator not configured")
	}
	var payload MergePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal wp merge payload: %w", err)
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
		OverrideDisabled:  payload.OverrideDisabled,
		Plan:              payload.Plan,
	}

	if err := p.wpOrchestrator.Execute(ctx, req); err != nil {
		if p.notifSvc != nil {
			p.notifSvc.OnMergeFailed(ctx, merchantID, err)
		}
		return err
	}

	// Remove secondary rows from the local cache. The WP plugin deletes the
	// secondary WP user accounts after merge, so they will never re-appear in a
	// sync payload. Without this cleanup the detector re-flags them as duplicates
	// on the next scan because the cache still has their stale records.
	if p.customerCacheRepo != nil {
		for _, sid := range payload.SecondaryIDs {
			if err := p.customerCacheRepo.DeleteByExternalID(ctx, merchantID, "wordpress", sid); err != nil {
				p.log.Warn().Err(err).
					Int64("secondary_id", sid).
					Str("merchant_id", merchantID.String()).
					Msg("wp merge: failed to remove secondary from cache — may re-appear in next scan")
			}
		}
	}

	if p.notifSvc != nil {
		p.notifSvc.OnMergeComplete(ctx, merchantID, payload.PrimaryCustomerID, len(payload.SecondaryIDs))
	}
	return nil
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
