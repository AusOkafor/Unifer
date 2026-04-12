package merge

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	snapshotsvc "merger/backend/internal/services/snapshot"
	shopifysvc "merger/backend/internal/services/shopify"
)

// MergeRequest holds the inputs for a merge operation.
type MergeRequest struct {
	MerchantID        uuid.UUID
	GroupID           uuid.UUID
	PrimaryCustomerID int64
	SecondaryIDs      []int64
	PerformedBy       string
}

// Orchestrator coordinates the full merge pipeline:
// snapshot → validate → execute (customerMerge) → audit.
type Orchestrator struct {
	validator    *Validator
	executor     *Executor
	snapshotSvc  *snapshotsvc.Service
	mergeRepo    repository.MergeRepository
	duplicateRepo repository.DuplicateRepository
	customerSvc  *shopifysvc.CustomerService
	log          zerolog.Logger
}

func NewOrchestrator(
	validator *Validator,
	executor *Executor,
	snapshotSvc *snapshotsvc.Service,
	mergeRepo repository.MergeRepository,
	duplicateRepo repository.DuplicateRepository,
	customerSvc *shopifysvc.CustomerService,
	log zerolog.Logger,
) *Orchestrator {
	return &Orchestrator{
		validator:    validator,
		executor:     executor,
		snapshotSvc:  snapshotSvc,
		mergeRepo:    mergeRepo,
		duplicateRepo: duplicateRepo,
		customerSvc:  customerSvc,
		log:          log,
	}
}

// Execute runs the full merge pipeline for a given request.
func (o *Orchestrator) Execute(ctx context.Context, req MergeRequest) error {
	allIDs := append([]int64{req.PrimaryCustomerID}, req.SecondaryIDs...)
	log := o.log.With().
		Str("merchant", req.MerchantID.String()).
		Int64("primary", req.PrimaryCustomerID).
		Logger()

	// Step 1: Fetch customer data from Shopify for validation
	log.Info().Msg("merge: fetching customer data")
	customers := make([]shopifysvc.ShopifyCustomer, 0, len(allIDs))
	for _, id := range allIDs {
		c, err := o.customerSvc.FetchByID(ctx, id)
		if err != nil {
			return fmt.Errorf("merge: fetch customer %d: %w", id, err)
		}
		customers = append(customers, *c)
	}

	// Step 2: Validate
	log.Info().Msg("merge: validating")
	if err := o.validator.Validate(ctx, customers); err != nil {
		return fmt.Errorf("merge validation failed: %w", err)
	}

	// Step 3: Snapshot (MUST happen before any mutation — merge is irreversible)
	log.Info().Msg("merge: creating snapshot")
	snap, err := o.snapshotSvc.Create(ctx, req.MerchantID, allIDs)
	if err != nil {
		return fmt.Errorf("merge: snapshot failed: %w", err)
	}
	log.Info().Str("snapshot_id", snap.ID.String()).Msg("merge: snapshot created")

	// Step 4: Execute via Shopify customerMerge GraphQL
	log.Info().Msg("merge: executing customerMerge")
	result, err := o.executor.Execute(ctx, req.PrimaryCustomerID, req.SecondaryIDs)
	if err != nil {
		// Snapshot exists — log for manual recovery
		log.Error().Err(err).Str("snapshot_id", snap.ID.String()).
			Msg("merge: execute failed — snapshot preserved for recovery")
		return fmt.Errorf("merge execute failed (snapshot %s preserved): %w", snap.ID, err)
	}

	log.Info().Str("resulting_gid", result.ResultingCustomerGID).Msg("merge: customerMerge succeeded")

	// Step 4b: Post-merge validation — re-fetch the surviving customer to confirm
	// expected fields are present. Anomalies are logged but never block the audit step.
	o.validatePostMerge(ctx, result.ResultingCustomerGID, req.PrimaryCustomerID, log)

	// Step 5: Audit record
	mergeRecord := &models.MergeRecord{
		MerchantID:           req.MerchantID,
		PrimaryCustomerID:    req.PrimaryCustomerID,
		SecondaryCustomerIDs: req.SecondaryIDs,
		OrdersMoved:          0, // Shopify handles this — we track logical count if needed
		PerformedBy:          req.PerformedBy,
		SnapshotID:           &snap.ID,
	}

	if err := o.mergeRepo.Create(ctx, mergeRecord); err != nil {
		log.Error().Err(err).Msg("merge: audit record creation failed")
		// Non-fatal — merge already succeeded in Shopify
	}

	// Step 6: Mark duplicate group as merged
	if req.GroupID != uuid.Nil {
		if err := o.duplicateRepo.UpdateStatus(ctx, req.GroupID, "merged"); err != nil {
			log.Warn().Err(err).Str("group_id", req.GroupID.String()).Msg("merge: update group status failed")
		}
	}

	log.Info().Str("merge_record_id", mergeRecord.ID.String()).Msg("merge: complete")
	return nil
}

// validatePostMerge re-fetches the surviving customer after a merge and logs
// any anomalies. It is non-blocking — a validation failure never fails the merge.
func (o *Orchestrator) validatePostMerge(ctx context.Context, resultingGID string, _ int64, log zerolog.Logger) {
	shopifyID, err := shopifysvc.GIDToShopifyID(resultingGID)
	if err != nil {
		log.Warn().Str("gid", resultingGID).Msg("post-merge validation: could not parse resulting GID")
		return
	}

	customer, err := o.customerSvc.FetchByID(ctx, shopifyID)
	if err != nil {
		log.Warn().Err(err).Int64("shopify_id", shopifyID).Msg("post-merge validation: could not fetch resulting customer")
		return
	}

	// Check that expected primary data is present
	if customer.Email == "" {
		log.Warn().Int64("shopify_id", shopifyID).Msg("post-merge validation: resulting customer has no email — data may have been overwritten")
	}
	if customer.FirstName == "" && customer.LastName == "" {
		log.Warn().Int64("shopify_id", shopifyID).Msg("post-merge validation: resulting customer has no name")
	}

	log.Info().
		Int64("shopify_id", shopifyID).
		Str("email", customer.Email).
		Int("orders_count", customer.OrdersCount).
		Msg("post-merge validation: resulting customer verified")
}
