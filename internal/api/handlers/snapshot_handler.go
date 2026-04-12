package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/middleware"
	"merger/backend/internal/services/jobs"
)

type SnapshotHandler struct {
	snapshotRepo repository.SnapshotRepository
	dispatcher   *jobs.Dispatcher
	log          zerolog.Logger
}

func NewSnapshotHandler(snapshotRepo repository.SnapshotRepository, dispatcher *jobs.Dispatcher, log zerolog.Logger) *SnapshotHandler {
	return &SnapshotHandler{
		snapshotRepo: snapshotRepo,
		dispatcher:   dispatcher,
		log:          log,
	}
}

func (h *SnapshotHandler) Restore(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	snapshotID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid snapshot id"})
		return
	}

	snap, err := h.snapshotRepo.FindByID(c.Request.Context(), snapshotID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
		return
	}

	if snap.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	jobID, err := h.dispatcher.Dispatch(
		c.Request.Context(),
		models.JobTypeRestoreSnapshot,
		merchant.ID,
		jobs.RestorePayload{
			SnapshotID: snapshotID.String(),
			MerchantID: merchant.ID.String(),
		},
	)
	if err != nil {
		h.log.Error().Err(err).Str("snapshot_id", snapshotID.String()).Msg("dispatch restore job")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue restore"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":  jobID.String(),
		"status":  models.JobStatusQueued,
		"message": "Restore job queued. Note: Shopify merges are irreversible — this will reconstruct customer data from the snapshot.",
	})
}
