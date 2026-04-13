package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/api/dto"
	"merger/backend/internal/middleware"
	"merger/backend/internal/repository"
	"merger/backend/internal/services/intelligence"
)

type DuplicateHandler struct {
	duplicateRepo     repository.DuplicateRepository
	customerCacheRepo repository.CustomerCacheRepository
	settingsRepo      repository.SettingsRepository
	log               zerolog.Logger
}

func NewDuplicateHandler(
	duplicateRepo repository.DuplicateRepository,
	customerCacheRepo repository.CustomerCacheRepository,
	settingsRepo repository.SettingsRepository,
	log zerolog.Logger,
) *DuplicateHandler {
	return &DuplicateHandler{
		duplicateRepo:     duplicateRepo,
		customerCacheRepo: customerCacheRepo,
		settingsRepo:      settingsRepo,
		log:               log,
	}
}

func (h *DuplicateHandler) List(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	status := c.Query("status")
	limit := 20
	offset := 0

	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}

	// Apply the merchant's confidence threshold from settings.
	// Default to 0 (show all) if settings haven't been saved yet.
	minConfidence := 0.0
	if h.settingsRepo != nil {
		if s, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID); err == nil {
			minConfidence = float64(s.ConfidenceThreshold) / 100.0
		}
	}

	groups, total, err := h.duplicateRepo.ListByMerchant(c.Request.Context(), merchant.ID, status, minConfidence, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("list duplicates")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list duplicates"})
		return
	}

	items := make([]dto.DuplicateGroupResponse, len(groups))
	for i, g := range groups {
		items[i] = dto.DuplicateGroupResponse{
			ID:             g.ID.String(),
			Confidence:     g.ConfidenceScore,
			ReadinessScore: g.ReadinessScore,
			Status:         g.Status,
			CustomerIDs:    []int64(g.CustomerIDs),
			CreatedAt:      g.CreatedAt,
		}
	}

	c.JSON(http.StatusOK, dto.PaginatedDuplicates{
		Data:   items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

func (h *DuplicateHandler) Get(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	group, err := h.duplicateRepo.FindByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "duplicate group not found"})
		return
	}

	if group.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	resp := dto.DuplicateGroupDetailResponse{
		DuplicateGroupResponse: dto.DuplicateGroupResponse{
			ID:             group.ID.String(),
			Confidence:     group.ConfidenceScore,
			ReadinessScore: group.ReadinessScore,
			Status:         group.Status,
			CustomerIDs:    []int64(group.CustomerIDs),
			CreatedAt:      group.CreatedAt,
		},
	}

	// Enrich with cached customer details
	if h.customerCacheRepo != nil {
		ctx := c.Request.Context()
		for _, shopifyID := range group.CustomerIDs {
			cached, err := h.customerCacheRepo.FindByShopifyID(ctx, merchant.ID, shopifyID)
			if err != nil {
				h.log.Debug().Int64("shopify_id", shopifyID).Err(err).Msg("get duplicate: customer cache miss")
				continue
			}
			tags := []string(cached.Tags)
			if tags == nil {
				tags = []string{}
			}
			resp.Customers = append(resp.Customers, dto.CustomerDetailDTO{
				ShopifyCustomerID: cached.ShopifyCustomerID,
				Name:              cached.Name,
				Email:             cached.Email,
				Phone:             cached.Phone,
				Tags:              tags,
				OrdersCount:       cached.OrdersCount,
				TotalSpent:        cached.TotalSpent,
				AddressJSON:       cached.AddressJSON,
				Note:              cached.Note,
				State:             cached.State,
				VerifiedEmail:     cached.VerifiedEmail,
				ShopifyCreatedAt:  cached.ShopifyCreatedAt,
			})
		}
	}

	// Deserialize stored intelligence report if present
	if len(group.IntelligenceJSON) > 0 {
		if report, err := intelligence.FromRawJSON(group.IntelligenceJSON); err == nil {
			resp.Intelligence = buildIntelligenceDTO(report)
		}
	}

	c.JSON(http.StatusOK, resp)
}

// Dismiss marks a duplicate group as dismissed (not a real duplicate).
// Dismissed groups are excluded from the default list view.
func (h *DuplicateHandler) Dismiss(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	group, err := h.duplicateRepo.FindByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "duplicate group not found"})
		return
	}

	if group.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	if err := h.duplicateRepo.UpdateStatus(c.Request.Context(), id, "dismissed"); err != nil {
		h.log.Error().Err(err).Str("group_id", id.String()).Msg("dismiss group")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to dismiss group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "dismissed"})
}

// buildIntelligenceDTO converts an IntelligenceReport to the API DTO.
func buildIntelligenceDTO(r *intelligence.IntelligenceReport) *dto.IntelligenceDTO {
	fieldConflicts := make([]dto.FieldConflictDTO, 0, len(r.Simulation.FieldConflicts))
	for _, fc := range r.Simulation.FieldConflicts {
		fieldConflicts = append(fieldConflicts, dto.FieldConflictDTO{
			Field:  fc.Field,
			Values: fc.Values,
		})
	}
	mergedTags := r.Simulation.MergedTags
	if mergedTags == nil {
		mergedTags = []string{}
	}
	sim := dto.SimulationDTO{
		SurvivingCustomerID: r.Simulation.SurvivingCustomerID,
		TotalOrderCount:     r.Simulation.TotalOrderCount,
		MergedTags:          mergedTags,
		FieldConflicts:      fieldConflicts,
	}
	reasoning := r.Reasoning
	if reasoning == nil {
		reasoning = []string{}
	}
	riskFlags := r.RiskFlags
	if riskFlags == nil {
		riskFlags = []string{}
	}
	return &dto.IntelligenceDTO{
		RecommendedPrimary: r.RecommendedPrimary,
		ReadinessScore:     r.ReadinessScore,
		Reasoning:          reasoning,
		RiskFlags:          riskFlags,
		Simulation:         sim,
	}
}
