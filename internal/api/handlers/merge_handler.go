package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/api/dto"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/middleware"
	billingpkg "merger/backend/internal/services/billing"
	"merger/backend/internal/services/intelligence"
	"merger/backend/internal/services/jobs"
	mergesvc "merger/backend/internal/services/merge"
)

type MergeHandler struct {
	mergeRepo         repository.MergeRepository
	snapshotRepo      repository.SnapshotRepository
	duplicateRepo     repository.DuplicateRepository
	customerCacheRepo repository.CustomerCacheRepository
	settingsRepo      repository.SettingsRepository
	dispatcher        *jobs.Dispatcher
	log               zerolog.Logger
}

func NewMergeHandler(
	mergeRepo repository.MergeRepository,
	snapshotRepo repository.SnapshotRepository,
	duplicateRepo repository.DuplicateRepository,
	customerCacheRepo repository.CustomerCacheRepository,
	settingsRepo repository.SettingsRepository,
	dispatcher *jobs.Dispatcher,
	log zerolog.Logger,
) *MergeHandler {
	return &MergeHandler{
		mergeRepo:         mergeRepo,
		snapshotRepo:      snapshotRepo,
		duplicateRepo:     duplicateRepo,
		customerCacheRepo: customerCacheRepo,
		settingsRepo:      settingsRepo,
		dispatcher:        dispatcher,
		log:               log,
	}
}

func (h *MergeHandler) Execute(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	// Enforce monthly merge limit before accepting the request.
	settings, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID)
	if err == nil {
		if err := billingpkg.CheckMergeAllowed(settings.Plan, settings.MergesThisMonth); err != nil {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "MERGE_LIMIT_REACHED",
				"message": "Monthly merge limit reached — upgrade your plan to continue merging.",
				"plan":    settings.Plan,
			})
			return
		}
	}
	// If settings lookup fails, proceed — don't block merges on a DB hiccup.

	var req dto.MergeExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.SecondaryIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one secondary customer ID required"})
		return
	}

	groupUUID, err := uuid.Parse(req.GroupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}

	g, err := h.duplicateRepo.FindByID(c.Request.Context(), groupUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "duplicate group not found"})
			return
		}
		h.log.Error().Err(err).Msg("merge execute: load duplicate group")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load duplicate group"})
		return
	}
	if g.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}
	if g.Status == "merged" {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "ALREADY_MERGED",
			"message": "Duplicate group already merged",
		})
		return
	}

	payload := jobs.MergePayload{
		MerchantID:        merchant.ID.String(),
		GroupID:           req.GroupID,
		PrimaryCustomerID: req.PrimaryCustomerID,
		SecondaryIDs:      req.SecondaryIDs,
		PerformedBy:       merchant.ShopDomain, // default attribution
		OverrideDisabled:  req.OverrideDisabled,
	}

	jobID, err := h.dispatcher.Dispatch(
		c.Request.Context(),
		models.JobTypeMergeCustomers,
		merchant.ID,
		payload,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("dispatch merge job")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue merge"})
		return
	}

	// Increment the monthly merge counter after successful dispatch.
	// Non-fatal: a DB hiccup here should not fail the already-accepted merge.
	if err := h.settingsRepo.IncrementMergeCount(c.Request.Context(), merchant.ID); err != nil {
		h.log.Warn().Err(err).Str("shop", merchant.ShopDomain).Msg("merge execute: increment merge count failed")
	}

	c.JSON(http.StatusAccepted, dto.MergeExecuteResponse{
		JobID:  jobID.String(),
		Status: models.JobStatusQueued,
	})
}

func (h *MergeHandler) History(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	settings, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID)
	if err == nil && !billingpkg.IsFeatureEnabled(settings.Plan, billingpkg.FeatureMergeHistory) {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":   "FEATURE_NOT_AVAILABLE",
			"message": "Merge history is available on the Basic plan and above.",
			"plan":    settings.Plan,
		})
		return
	}

	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}

	records, total, err := h.mergeRepo.ListByMerchant(c.Request.Context(), merchant.ID, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("list merge history")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load history"})
		return
	}

	items := make([]dto.MergeRecordResponse, len(records))
	for i, r := range records {
		item := dto.MergeRecordResponse{
			ID:                   r.ID.String(),
			PrimaryCustomerID:    r.PrimaryCustomerID,
			SecondaryCustomerIDs: []int64(r.SecondaryCustomerIDs),
			OrdersMoved:          r.OrdersMoved,
			PerformedBy:          r.PerformedBy,
			ConfidenceSource:     r.ConfidenceSource,
			OverrideUsed:         r.OverrideUsed,
			CreatedAt:            r.CreatedAt,
		}
		if r.SnapshotID != nil {
			s := r.SnapshotID.String()
			item.SnapshotID = &s
			ok, err := h.snapshotRepo.Exists(c.Request.Context(), *r.SnapshotID)
			if err != nil {
				h.log.Error().Err(err).Str("snapshot_id", s).Msg("snapshot exists check for history")
				// Omit snapshot_available on error — client treats unknown as "try opening"
			} else {
				item.SnapshotAvailable = &ok
			}
		}
		items[i] = item
	}

	c.JSON(http.StatusOK, dto.PaginatedMergeRecords{
		Data:   items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// BulkPreview returns aggregate statistics for all safe pending groups so the
// operator can review the blast radius before triggering SafeBulkMerge.
func (h *MergeHandler) BulkPreview(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	if settings, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID); err == nil {
		if !billingpkg.IsFeatureEnabled(settings.Plan, billingpkg.FeatureBulkMerge) {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "FEATURE_NOT_AVAILABLE",
				"message": "Bulk merge is available on the Pro plan.",
				"plan":    settings.Plan,
			})
			return
		}
	}

	groups, err := h.duplicateRepo.ListSafeGroups(c.Request.Context(), merchant.ID)
	if err != nil {
		h.log.Error().Err(err).Msg("bulk preview: list safe groups")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load safe groups"})
		return
	}

	preview := dto.BulkPreviewResponse{
		SafeGroupCount: len(groups),
	}

	customerSet := make(map[int64]struct{})
	for _, g := range groups {
		for _, id := range g.CustomerIDs {
			customerSet[id] = struct{}{}
		}

		if len(g.IntelligenceJSON) > 0 {
			if report, err := intelligence.FromRawJSON(g.IntelligenceJSON); err == nil {
				preview.CombinedOrders += report.Simulation.TotalOrderCount
				preview.ConflictCount += len(report.Simulation.FieldConflicts)
			}
		}
	}
	preview.TotalCustomers = len(customerSet)

	c.JSON(http.StatusOK, preview)
}

// SafeBulkMerge queues merge jobs for safe pending duplicate groups.
// Use the ?limit= query param (default 10, max 50) to control batch size and
// avoid spiking the Shopify rate limit or flooding the job queue. Call
// repeatedly (incrementing offset via the returned queued count) to page
// through all safe groups.
func (h *MergeHandler) SafeBulkMerge(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	if settings, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID); err == nil {
		if !billingpkg.IsFeatureEnabled(settings.Plan, billingpkg.FeatureBulkMerge) {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "FEATURE_NOT_AVAILABLE",
				"message": "Bulk merge is available on the Pro plan.",
				"plan":    settings.Plan,
			})
			return
		}
	}

	// Batch size control — prevents rate limit spikes and queue congestion.
	batchSize := 10
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		if l > 50 {
			l = 50
		}
		batchSize = l
	}

	groups, err := h.duplicateRepo.ListSafeGroups(c.Request.Context(), merchant.ID)
	if err != nil {
		h.log.Error().Err(err).Msg("safe bulk merge: list safe groups")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load safe groups"})
		return
	}

	if len(groups) == 0 {
		c.JSON(http.StatusOK, dto.SafeBulkMergeResponse{})
		return
	}

	// Apply batch limit so callers can page through large queues incrementally.
	if len(groups) > batchSize {
		groups = groups[:batchSize]
	}

	resp := dto.SafeBulkMergeResponse{}
	for _, g := range groups {
		// Determine primary from the stored intelligence report.
		var primaryID int64
		if len(g.IntelligenceJSON) > 0 {
			if report, err := intelligence.FromRawJSON(g.IntelligenceJSON); err == nil {
				primaryID = report.RecommendedPrimary
			}
		}
		if primaryID == 0 {
			// No intelligence report — skip rather than guess.
			resp.Skipped++
			continue
		}

		secondaryIDs := make([]int64, 0, len(g.CustomerIDs)-1)
		for _, id := range g.CustomerIDs {
			if id != primaryID {
				secondaryIDs = append(secondaryIDs, id)
			}
		}
		if len(secondaryIDs) == 0 {
			resp.Skipped++
			continue
		}

		payload := jobs.MergePayload{
			MerchantID:        merchant.ID.String(),
			GroupID:           g.ID.String(),
			PrimaryCustomerID: primaryID,
			SecondaryIDs:      secondaryIDs,
			PerformedBy:       merchant.ShopDomain + " (bulk)",
		}
		jobID, err := h.dispatcher.Dispatch(
			c.Request.Context(),
			models.JobTypeMergeCustomers,
			merchant.ID,
			payload,
		)
		if err != nil {
			h.log.Warn().Err(err).Str("group_id", g.ID.String()).Msg("safe bulk merge: dispatch failed")
			resp.Skipped++
			continue
		}
		resp.JobIDs = append(resp.JobIDs, jobID.String())
		resp.Queued++
	}

	c.JSON(http.StatusAccepted, resp)
}

// ValidateProfile loads the customers for the given IDs, runs conflict
// detection, and returns a split blocking/resolvable result so the frontend
// can render the correct BLOCKED / NEEDS_RESOLUTION / READY state after each
// Merge Composer field selection.
func (h *MergeHandler) ValidateProfile(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	var req dto.MergeValidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	allIDs := append([]int64{req.PrimaryCustomerID}, req.SecondaryIDs...)

	customers, err := h.customerCacheRepo.FindByShopifyIDs(c.Request.Context(), merchant.ID, allIDs)
	if err != nil {
		h.log.Error().Err(err).Msg("validate profile: load customers")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load customer data"})
		return
	}
	if len(customers) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "need at least 2 customers to validate"})
		return
	}

	sel := mergesvc.FieldSelection{
		Email:   req.Selection.Email,
		Phone:   req.Selection.Phone,
		Address: req.Selection.Address,
		Name:    req.Selection.Name,
	}
	result := mergesvc.ValidateFinalProfile(customers, sel, req.OverrideDisabled)

	// Ensure JSON arrays are never null.
	if result.BlockingConflicts == nil {
		result.BlockingConflicts = []intelligence.ConflictItem{}
	}
	if result.ResolvableConflicts == nil {
		result.ResolvableConflicts = []intelligence.ConflictItem{}
	}

	c.JSON(http.StatusOK, dto.MergeValidateResponse{
		HasBlockingConflicts: result.HasBlockingConflicts,
		BlockingConflicts:    result.BlockingConflicts,
		ResolvableConflicts:  result.ResolvableConflicts,
		IsReadyToMerge:      result.IsReadyToMerge,
	})
}
