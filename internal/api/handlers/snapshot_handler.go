package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/api/dto"
	"merger/backend/internal/middleware"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/services/jobs"
	shopifysvc "merger/backend/internal/services/shopify"
	snapshotsvc "merger/backend/internal/services/snapshot"
)

type SnapshotHandler struct {
	snapshotRepo repository.SnapshotRepository
	snapshotSvc  *snapshotsvc.Service
	dispatcher   *jobs.Dispatcher
	log          zerolog.Logger
}

func NewSnapshotHandler(
	snapshotRepo repository.SnapshotRepository,
	snapshotSvc *snapshotsvc.Service,
	dispatcher *jobs.Dispatcher,
	log zerolog.Logger,
) *SnapshotHandler {
	return &SnapshotHandler{
		snapshotRepo: snapshotRepo,
		snapshotSvc:  snapshotSvc,
		dispatcher:   dispatcher,
		log:          log,
	}
}

// Get returns snapshot metadata and customer rows for preview before restore.
func (h *SnapshotHandler) Get(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	snapshotID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid snapshot id"})
		return
	}

	snap, data, err := h.snapshotSvc.Get(c.Request.Context(), snapshotID)
	if err != nil {
		if errors.Is(err, snapshotsvc.ErrSnapshotNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "SNAPSHOT_NOT_FOUND",
				"message": "Snapshot not found",
			})
			return
		}
		h.log.Error().Err(err).Str("snapshot_id", snapshotID.String()).Msg("snapshot get for preview")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load snapshot"})
		return
	}

	if snap.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	customers := make([]dto.SnapshotCustomerPreview, 0, len(data.Customers))
	for _, sc := range data.Customers {
		customers = append(customers, snapshotCustomerToDTO(sc))
	}

	var mergeRecordID *string
	if snap.MergeRecordID != nil {
		s := snap.MergeRecordID.String()
		mergeRecordID = &s
	}

	c.JSON(http.StatusOK, dto.SnapshotPreviewResponse{
		SnapshotID:    snap.ID.String(),
		CreatedAt:     snap.CreatedAt,
		MergeRecordID: mergeRecordID,
		Customers:     customers,
	})
}

func snapshotCustomerToDTO(sc shopifysvc.ShopifyCustomer) dto.SnapshotCustomerPreview {
	var tags []string
	if sc.Tags != "" {
		for _, t := range strings.Split(sc.Tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}
	name := strings.TrimSpace(sc.FirstName + " " + sc.LastName)
	if name == "" {
		name = sc.Email
	}
	return dto.SnapshotCustomerPreview{
		ShopifyCustomerID: sc.ID,
		Email:             sc.Email,
		FirstName:         sc.FirstName,
		LastName:          sc.LastName,
		DisplayName:       name,
		Phone:             sc.Phone,
		Tags:              tags,
		OrdersCount:       sc.OrdersCount,
		TotalSpent:        sc.TotalSpent,
		CreatedAt:         sc.CreatedAt,
		AddressSummary:    formatAddressSummary(sc.Addresses),
	}
}

func formatAddressSummary(addrs []shopifysvc.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	a := addrs[0]
	var b strings.Builder
	if a.Address1 != "" {
		b.WriteString(a.Address1)
	}
	if a.Address2 != "" {
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		b.WriteString(a.Address2)
	}
	cityLine := strings.TrimSpace(strings.Join(nonEmptyParts(a.City, a.Province, a.Zip), ", "))
	if cityLine != "" {
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(cityLine)
	}
	if a.Country != "" {
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(a.Country)
	}
	return b.String()
}

func nonEmptyParts(parts ...string) []string {
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "SNAPSHOT_NOT_FOUND",
				"message": "Snapshot not found",
			})
			return
		}
		h.log.Error().Err(err).Str("snapshot_id", snapshotID.String()).Msg("snapshot find for restore")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load snapshot"})
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
