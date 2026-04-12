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
	"merger/backend/internal/services/jobs"
)

type MergeHandler struct {
	mergeRepo  repository.MergeRepository
	dispatcher *jobs.Dispatcher
	log        zerolog.Logger
}

func NewMergeHandler(mergeRepo repository.MergeRepository, dispatcher *jobs.Dispatcher, log zerolog.Logger) *MergeHandler {
	return &MergeHandler{
		mergeRepo:  mergeRepo,
		dispatcher: dispatcher,
		log:        log,
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

// needed for uuid.Nil check in other handlers
var _ = uuid.Nil
