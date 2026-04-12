package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"merger/backend/internal/middleware"
	"merger/backend/internal/models"
	"merger/backend/internal/services/jobs"
)

type ScanHandler struct {
	dispatcher *jobs.Dispatcher
	log        zerolog.Logger
}

func NewScanHandler(dispatcher *jobs.Dispatcher, log zerolog.Logger) *ScanHandler {
	return &ScanHandler{dispatcher: dispatcher, log: log}
}

// Trigger queues a sync_customers job for the authenticated merchant.
// The worker will fetch all Shopify customers, populate the cache,
// then automatically queue detect_duplicates.
func (h *ScanHandler) Trigger(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	jobID, err := h.dispatcher.Dispatch(c.Request.Context(),
		models.JobTypeSyncCustomers,
		merchant.ID,
		map[string]string{"merchant_id": merchant.ID.String()},
	)
	if err != nil {
		h.log.Error().Err(err).Msg("scan: dispatch failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue scan"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"job_id": jobID,
		"status": "queued",
		"message": "Customer sync started — duplicate detection will run automatically after.",
	})
}
