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

	// Collect all unique customer IDs across every group in this page so we can
	// enrich the list with names+emails in a single DB query instead of N queries.
	allShopifyIDs := make([]int64, 0)
	seen := make(map[int64]struct{})
	for _, g := range groups {
		for _, id := range g.CustomerIDs {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				allShopifyIDs = append(allShopifyIDs, id)
			}
		}
	}
	summaryMap := make(map[int64]dto.CustomerSummaryDTO)
	if h.customerCacheRepo != nil && len(allShopifyIDs) > 0 {
		if cached, err2 := h.customerCacheRepo.FindByShopifyIDs(c.Request.Context(), merchant.ID, allShopifyIDs); err2 == nil {
			for _, cc := range cached {
				summaryMap[cc.ShopifyCustomerID] = dto.CustomerSummaryDTO{
					ShopifyCustomerID: cc.ShopifyCustomerID,
					Name:              cc.Name,
					Email:             cc.Email,
				}
			}
		}
	}

	items := make([]dto.DuplicateGroupResponse, len(groups))
	for i, g := range groups {
		summaries := make([]dto.CustomerSummaryDTO, 0, len(g.CustomerIDs))
		for _, id := range g.CustomerIDs {
			if s, ok := summaryMap[id]; ok {
				summaries = append(summaries, s)
			}
		}
		items[i] = dto.DuplicateGroupResponse{
			ID:                g.ID.String(),
			Confidence:        g.ConfidenceScore,
			RiskLevel:         g.RiskLevel,
			ReadinessScore:    g.ReadinessScore,
			Status:            g.Status,
			CustomerIDs:       []int64(g.CustomerIDs),
			CustomerSummaries: summaries,
			CreatedAt:         g.CreatedAt,
			BusinessRiskLevel: g.BusinessRiskLevel,
			ImpactScore:       g.ImpactScore,
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
			ID:                group.ID.String(),
			Confidence:        group.ConfidenceScore,
			RiskLevel:         group.RiskLevel,
			ReadinessScore:    group.ReadinessScore,
			Status:            group.Status,
			CustomerIDs:       []int64(group.CustomerIDs),
			CreatedAt:         group.CreatedAt,
			BusinessRiskLevel: group.BusinessRiskLevel,
			ImpactScore:       group.ImpactScore,
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
// Accepts an optional JSON body: {"reason": "different_people" | "same_person_keep_separate" | "data_error" | "other"}
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

	var body struct {
		Reason string `json:"reason"`
	}
	// Ignore parse errors — reason is optional.
	_ = c.ShouldBindJSON(&body)

	if err := h.duplicateRepo.DismissGroup(c.Request.Context(), id, body.Reason); err != nil {
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

	// Map structured conflict items.
	conflicts := make([]dto.ConflictItemDTO, 0, len(r.Conflicts))
	for _, ci := range r.Conflicts {
		conflicts = append(conflicts, dto.ConflictItemDTO{
			Type:       ci.Type,
			Severity:   ci.Severity,
			Blocking:   ci.Blocking,
			Resolvable: ci.Resolvable,
		})
	}

	idto := &dto.IntelligenceDTO{
		RecommendedPrimary: r.RecommendedPrimary,
		ReadinessScore:     r.ReadinessScore,
		Reasoning:          reasoning,
		RiskFlags:          riskFlags,
		Simulation:         sim,
		Conflicts:          conflicts,
		ConflictSeverity:   r.ConflictSeverity,
		Summary:            r.Summary,
		ConfidenceSource:   r.ConfidenceSource,
	}

	if r.Breakdown != nil {
		reasons := make([]dto.ReasonItemDTO, 0, len(r.Breakdown.Reasons))
		for _, ri := range r.Breakdown.Reasons {
			reasons = append(reasons, dto.ReasonItemDTO{
				Text:       ri.Text,
				Importance: ri.Importance,
			})
		}
		idto.Breakdown = &dto.FieldBreakdownDTO{
			EmailScore:   r.Breakdown.EmailScore,
			NameScore:    r.Breakdown.NameScore,
			PhoneScore:   r.Breakdown.PhoneScore,
			AddressScore: r.Breakdown.AddressScore,
			Reasons:      reasons,
		}
	}

	if r.BehavioralSignals != nil {
		idto.BehavioralSignals = &dto.BehavioralSignalsDTO{
			OrderAddressExact:    r.BehavioralSignals.OrderAddressExact,
			OrderAddressPartial:  r.BehavioralSignals.OrderAddressPartial,
			OrderNameHigh:        r.BehavioralSignals.OrderNameHigh,
			RecentOrderOverlap:   r.BehavioralSignals.RecentOrderOverlap,
			OrderNameConflict:    r.BehavioralSignals.OrderNameConflict,
			OrderAddressConflict: r.BehavioralSignals.OrderAddressConflict,
		}
	}

	return idto
}
