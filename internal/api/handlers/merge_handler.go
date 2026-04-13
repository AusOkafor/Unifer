package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/api/dto"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/middleware"
	"merger/backend/internal/services/intelligence"
	"merger/backend/internal/services/jobs"
)

type MergeHandler struct {
	mergeRepo     repository.MergeRepository
	duplicateRepo repository.DuplicateRepository
	dispatcher    *jobs.Dispatcher
	log           zerolog.Logger
}

func NewMergeHandler(
	mergeRepo repository.MergeRepository,
	duplicateRepo repository.DuplicateRepository,
	dispatcher *jobs.Dispatcher,
	log zerolog.Logger,
) *MergeHandler {
	return &MergeHandler{
		mergeRepo:     mergeRepo,
		duplicateRepo: duplicateRepo,
		dispatcher:    dispatcher,
		log:           log,
	}
}

func (h *MergeHandler) Execute(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	var req dto.MergeExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.SecondaryIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one secondary customer ID required"})
		return
	}

	payload := jobs.MergePayload{
		MerchantID:        merchant.ID.String(),
		GroupID:           req.GroupID,
		PrimaryCustomerID: req.PrimaryCustomerID,
		SecondaryIDs:      req.SecondaryIDs,
		PerformedBy:       merchant.ShopDomain, // default attribution
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

	c.JSON(http.StatusAccepted, dto.MergeExecuteResponse{
		JobID:  jobID.String(),
		Status: models.JobStatusQueued,
	})
}

func (h *MergeHandler) History(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

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
			CreatedAt:            r.CreatedAt,
		}
		if r.SnapshotID != nil {
			s := r.SnapshotID.String()
			item.SnapshotID = &s
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

// SafeBulkMerge queues merge jobs for every safe pending duplicate group.
// Groups without a clear recommended primary are skipped.
func (h *MergeHandler) SafeBulkMerge(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

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

// needed for uuid.Nil check in other handlers
var _ = uuid.Nil
