package merge

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/services/intelligence"
	snapshotsvc "merger/backend/internal/services/snapshot"
	shopifysvc "merger/backend/internal/services/shopify"
	"merger/backend/internal/utils"
)

// MergeRequest holds the inputs for a merge operation.
type MergeRequest struct {
	MerchantID        uuid.UUID
	GroupID           uuid.UUID
	PrimaryCustomerID int64
	SecondaryIDs      []int64
	PerformedBy       string
	// OverrideDisabled is true when the user explicitly bypassed the
	// disabled_account hard block. Recorded in audit logs for traceability.
	OverrideDisabled  bool
}

// Orchestrator coordinates the full merge pipeline:
// snapshot → validate → execute (customerMerge) → audit.
type Orchestrator struct {
	validator         *Validator
	snapshotSvc       *snapshotsvc.Service
	mergeRepo         repository.MergeRepository
	duplicateRepo     repository.DuplicateRepository
	customerCacheRepo repository.CustomerCacheRepository
	merchantRepo      repository.MerchantRepository
	encryptor         *utils.Encryptor
	log               zerolog.Logger
}

func NewOrchestrator(
	validator *Validator,
	snapshotSvc *snapshotsvc.Service,
	mergeRepo repository.MergeRepository,
	duplicateRepo repository.DuplicateRepository,
	customerCacheRepo repository.CustomerCacheRepository,
	merchantRepo repository.MerchantRepository,
	encryptor *utils.Encryptor,
	log zerolog.Logger,
) *Orchestrator {
	return &Orchestrator{
		validator:         validator,
		snapshotSvc:       snapshotSvc,
		mergeRepo:         mergeRepo,
		duplicateRepo:     duplicateRepo,
		customerCacheRepo: customerCacheRepo,
		merchantRepo:      merchantRepo,
		encryptor:         encryptor,
		log:               log,
	}
}

// Execute runs the full merge pipeline for a given request.
func (o *Orchestrator) Execute(ctx context.Context, req MergeRequest) error {
	log := o.log.With().
		Str("merchant", req.MerchantID.String()).
		Int64("primary", req.PrimaryCustomerID).
		Logger()

	// Build a real per-merchant Shopify client.
	merchant, err := o.merchantRepo.FindByID(ctx, req.MerchantID)
	if err != nil {
		return fmt.Errorf("merge: load merchant: %w", err)
	}
	token, err := o.encryptor.Decrypt(merchant.AccessTokenEnc)
	if err != nil {
		return fmt.Errorf("merge: decrypt token: %w", err)
	}
	shopifyClient := shopifysvc.NewClient(merchant.ShopDomain, token, o.log)
	customerSvc := shopifysvc.NewCustomerService(shopifyClient)

	// Step 1: Load customer data from cache for validation.
	log.Info().Msg("merge: loading customer data from cache")
	allIDs := append([]int64{req.PrimaryCustomerID}, req.SecondaryIDs...)
	cacheCustomers, err := o.loadCacheCustomers(ctx, req.MerchantID, allIDs)
	if err != nil {
		return fmt.Errorf("merge: load cache customers: %w", err)
	}

	// Step 2: Validate.
	log.Info().Msg("merge: validating")
	if req.OverrideDisabled {
		log.Warn().Msg("merge: disabled_account override accepted — user acknowledged reactivation risk")
	}
	if err := o.validator.Validate(ctx, cacheCustomers); err != nil {
		return fmt.Errorf("merge validation failed: %w", err)
	}

	// Step 3: Snapshot (MUST happen before any mutation — merge is irreversible).
	log.Info().Msg("merge: creating snapshot")
	snap, err := o.snapshotSvc.CreateFromCache(ctx, req.MerchantID, cacheCustomers)
	if err != nil {
		return fmt.Errorf("merge: snapshot failed: %w", err)
	}
	log.Info().Str("snapshot_id", snap.ID.String()).Msg("merge: snapshot created")

	// Capture expected combined order count BEFORE the merge so we can validate
	// Shopify consolidated them correctly afterwards.
	expectedMinOrders := 0
	for _, c := range cacheCustomers {
		expectedMinOrders += c.OrdersCount
	}

	// Step 4: Execute via Shopify customerMerge GraphQL.
	log.Info().Msg("merge: executing customerMerge")
	executor := NewExecutor(customerSvc)
	result, err := executor.Execute(ctx, req.PrimaryCustomerID, req.SecondaryIDs)
	if err != nil {
		log.Error().Err(err).Str("snapshot_id", snap.ID.String()).
			Msg("merge: execute failed — snapshot preserved for recovery")
		return fmt.Errorf("merge execute failed (snapshot %s preserved): %w", snap.ID, err)
	}

	log.Info().Str("resulting_gid", result.ResultingCustomerGID).Msg("merge: customerMerge succeeded")

	// Step 4b: Post-merge validation — non-blocking, best-effort.
	o.validatePostMerge(ctx, result.ResultingCustomerGID, expectedMinOrders, customerSvc, log)

	// Determine confidence source from the group's intelligence report (if available).
	confidenceSource := ""
	if req.GroupID != uuid.Nil {
		if group, err := o.duplicateRepo.FindByID(ctx, req.GroupID); err == nil && len(group.IntelligenceJSON) > 0 {
			if report, err := intelligence.FromRawJSON(group.IntelligenceJSON); err == nil {
				confidenceSource = report.ConfidenceSource
			}
		}
	}

	// Step 5: Audit record.
	mergeRecord := &models.MergeRecord{
		MerchantID:           req.MerchantID,
		PrimaryCustomerID:    req.PrimaryCustomerID,
		SecondaryCustomerIDs: req.SecondaryIDs,
		OrdersMoved:          0,
		PerformedBy:          req.PerformedBy,
		SnapshotID:           &snap.ID,
		ConfidenceSource:     confidenceSource,
	}

	if err := o.mergeRepo.Create(ctx, mergeRecord); err != nil {
		log.Error().Err(err).Msg("merge: audit record creation failed")
	}

	// Step 6: Mark duplicate group as merged + record learning signal.
	if req.GroupID != uuid.Nil {
		if err := o.duplicateRepo.UpdateStatus(ctx, req.GroupID, "merged"); err != nil {
			log.Warn().Err(err).Str("group_id", req.GroupID.String()).Msg("merge: update group status failed")
		}
		// confirmed_by_user=true when a human explicitly triggered the merge
		// (i.e. not a bulk/automated job). Bulk jobs set PerformedBy to include "(bulk)".
		isManual := !strings.Contains(req.PerformedBy, "(bulk)")
		if err := o.duplicateRepo.MarkConfirmedByUser(ctx, req.GroupID, isManual); err != nil {
			log.Warn().Err(err).Str("group_id", req.GroupID.String()).Msg("merge: mark confirmed_by_user failed")
		}
	}

	log.Info().Str("merge_record_id", mergeRecord.ID.String()).Msg("merge: complete")
	return nil
}

// loadCacheCustomers fetches CustomerCache rows for the given Shopify IDs.
func (o *Orchestrator) loadCacheCustomers(ctx context.Context, merchantID uuid.UUID, ids []int64) ([]models.CustomerCache, error) {
	all, err := o.customerCacheRepo.FindByMerchant(ctx, merchantID)
	if err != nil {
		return nil, err
	}
	index := make(map[int64]models.CustomerCache, len(all))
	for _, c := range all {
		index[c.ShopifyCustomerID] = c
	}
	result := make([]models.CustomerCache, 0, len(ids))
	for _, id := range ids {
		c, ok := index[id]
		if !ok {
			return nil, fmt.Errorf("customer %d not found in cache — run a sync first", id)
		}
		result = append(result, c)
	}
	return result, nil
}

// validatePostMerge re-fetches the surviving customer and checks that the merge
// was consistent. It is non-blocking — a failure logs a warning but never
// fails or rolls back the merge.
//
// Checks performed:
//   - Customer still exists (not 404)
//   - Email is present
//   - orders_count >= expectedMinOrders (confirms Shopify consolidated orders)
func (o *Orchestrator) validatePostMerge(
	ctx context.Context,
	resultingGID string,
	expectedMinOrders int,
	customerSvc *shopifysvc.CustomerService,
	log zerolog.Logger,
) {
	shopifyID, err := shopifysvc.GIDToShopifyID(resultingGID)
	if err != nil {
		log.Warn().Str("gid", resultingGID).Msg("post-merge validation: could not parse resulting GID")
		return
	}

	customer, err := customerSvc.FetchByID(ctx, shopifyID)
	if err != nil {
		// REST may be restricted (Protected Customer Data) — log and continue.
		log.Warn().Err(err).Int64("shopify_id", shopifyID).
			Msg("post-merge validation: REST fetch failed — skipping consistency check")
		return
	}

	ok := true

	if customer.Email == "" {
		log.Warn().Int64("shopify_id", shopifyID).
			Msg("post-merge validation: resulting customer has no email address")
		ok = false
	}

	if expectedMinOrders > 0 && customer.OrdersCount < expectedMinOrders {
		log.Warn().
			Int64("shopify_id", shopifyID).
			Int("orders_got", customer.OrdersCount).
			Int("orders_expected_min", expectedMinOrders).
			Msg("post-merge validation: WARNING — order count lower than expected; Shopify may still be consolidating")
		ok = false
	}

	if ok {
		log.Info().
			Int64("shopify_id", shopifyID).
			Str("email", customer.Email).
			Int("orders_count", customer.OrdersCount).
			Int("expected_min_orders", expectedMinOrders).
			Msg("post-merge validation: ok — customer consistent")
	}
}
