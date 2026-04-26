package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"merger/backend/internal/api/dto"
	billingpkg "merger/backend/internal/services/billing"
	"merger/backend/internal/middleware"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/services/intelligence"
	"merger/backend/internal/services/jobs"
	snapshotsvc "merger/backend/internal/services/snapshot"
	wpsvc "merger/backend/internal/services/wordpress"
	"merger/backend/internal/utils"
)

// WPHandler handles all WordPress merchant endpoints.
type WPHandler struct {
	merchantRepo      repository.MerchantRepository
	refreshRepo       repository.WPRefreshTokenRepository
	syncSvc           *wpsvc.SyncService
	dispatcher        *jobs.Dispatcher
	encryptor         *utils.Encryptor
	duplicateRepo     repository.DuplicateRepository
	customerCacheRepo repository.CustomerCacheRepository
	mergeRepo         repository.MergeRepository
	snapshotSvc       *snapshotsvc.Service
	settingsRepo      repository.SettingsRepository
	db                *sqlx.DB
	jwtSecret          string
	pluginVersion      string
	pluginDownloadURL  string
	log                zerolog.Logger
}

func NewWPHandler(
	merchantRepo      repository.MerchantRepository,
	refreshRepo       repository.WPRefreshTokenRepository,
	syncSvc           *wpsvc.SyncService,
	dispatcher        *jobs.Dispatcher,
	encryptor         *utils.Encryptor,
	duplicateRepo     repository.DuplicateRepository,
	customerCacheRepo repository.CustomerCacheRepository,
	mergeRepo         repository.MergeRepository,
	snapshotSvc       *snapshotsvc.Service,
	settingsRepo      repository.SettingsRepository,
	db                *sqlx.DB,
	jwtSecret         string,
	pluginVersion     string,
	pluginDownloadURL string,
	log               zerolog.Logger,
) *WPHandler {
	return &WPHandler{
		merchantRepo:      merchantRepo,
		refreshRepo:       refreshRepo,
		syncSvc:           syncSvc,
		dispatcher:        dispatcher,
		encryptor:         encryptor,
		duplicateRepo:     duplicateRepo,
		customerCacheRepo: customerCacheRepo,
		mergeRepo:         mergeRepo,
		snapshotSvc:       snapshotSvc,
		settingsRepo:      settingsRepo,
		db:                db,
		jwtSecret:         jwtSecret,
		pluginVersion:     pluginVersion,
		pluginDownloadURL: pluginDownloadURL,
		log:               log,
	}
}

// ─── Plugin version ──────────────────────────────────────────────────────────

// PluginVersion handles GET /api/wp/plugin/version (unauthenticated).
// The plugin polls this endpoint to drive WordPress's native update system.
func (h *WPHandler) PluginVersion(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":      h.pluginVersion,
		"download_url": h.pluginDownloadURL,
		"requires_wp":  "6.0",
		"requires_php": "7.4",
	})
}

// ─── Auth ────────────────────────────────────────────────────────────────────

// Register handles POST /api/wp/register (unauthenticated).
func (h *WPHandler) Register(c *gin.Context) {
	var req dto.WPRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	encKey, err := h.encryptor.Encrypt(req.APIKey)
	if err != nil {
		h.log.Error().Err(err).Msg("wp register: encrypt api key")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	merchant := &models.Merchant{
		ShopDomain:     req.SiteURL,
		AccessTokenEnc: encKey,
		Platform:       "wordpress",
	}
	if err := h.merchantRepo.Create(c.Request.Context(), merchant); err != nil {
		h.log.Error().Err(err).Str("site_url", req.SiteURL).Msg("wp register: upsert merchant")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register"})
		return
	}

	if err := h.refreshRepo.RevokeAll(c.Request.Context(), merchant.ID); err != nil {
		h.log.Warn().Err(err).Str("merchant_id", merchant.ID.String()).Msg("wp register: revoke old tokens")
	}

	accessToken, err := middleware.IssueAccessToken(h.jwtSecret, merchant.ID)
	if err != nil {
		h.log.Error().Err(err).Msg("wp register: issue access token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	raw, hash, expiresAt, err := middleware.IssueRefreshToken()
	if err != nil {
		h.log.Error().Err(err).Msg("wp register: issue refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if err := h.refreshRepo.Create(c.Request.Context(), &models.WPRefreshToken{
		MerchantID: merchant.ID,
		TokenHash:  hash,
		ExpiresAt:  expiresAt,
	}); err != nil {
		h.log.Error().Err(err).Msg("wp register: store refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, dto.WPRegisterResponse{
		AccessToken:  accessToken,
		RefreshToken: merchant.ID.String() + ":" + raw,
		ExpiresIn:    900,
	})
}

// Refresh handles POST /api/wp/auth/refresh (unauthenticated).
func (h *WPHandler) Refresh(c *gin.Context) {
	var req dto.WPRefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	merchantID, rawToken, ok := splitRefreshToken(req.RefreshToken)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	hash := middleware.HashRefreshToken(rawToken)
	stored, err := h.refreshRepo.FindValid(c.Request.Context(), merchantID, hash)
	if err != nil || stored == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token invalid or expired"})
		return
	}

	accessToken, err := middleware.IssueAccessToken(h.jwtSecret, merchantID)
	if err != nil {
		h.log.Error().Err(err).Msg("wp refresh: issue access token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, dto.WPRefreshResponse{
		AccessToken: accessToken,
		ExpiresIn:   900,
	})
}

// ─── Sync ────────────────────────────────────────────────────────────────────

// SyncUsers handles POST /api/wp/customers/sync.
func (h *WPHandler) SyncUsers(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req dto.WPSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn().Err(err).Str("merchant_id", merchant.ID.String()).Msg("wp sync: invalid request body — plugin may be sending 'users' instead of 'customers'")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Customers) == 0 {
		h.log.Warn().Str("merchant_id", merchant.ID.String()).Msg("wp sync: customers array is empty or missing")
		c.JSON(http.StatusBadRequest, gin.H{"error": "customers must not be empty"})
		return
	}

	ingested, err := h.syncSvc.IngestCustomers(c.Request.Context(), merchant.ID, req.Customers)
	if err != nil {
		h.log.Error().Err(err).Str("merchant_id", merchant.ID.String()).Msg("wp sync: ingest failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync failed"})
		return
	}

	var jobIDStr string
	if h.dispatcher != nil {
		jobID, err := h.dispatcher.Dispatch(c.Request.Context(), models.JobTypeDetectDuplicates, merchant.ID,
			map[string]string{"merchant_id": merchant.ID.String()})
		if err != nil {
			h.log.Warn().Err(err).Msg("wp sync: failed to dispatch detect job")
		} else {
			jobIDStr = jobID.String()
		}
	}

	c.JSON(http.StatusOK, dto.WPSyncResponse{
		Ingested: ingested,
		JobID:    jobIDStr,
	})
}

// ─── Dashboard ───────────────────────────────────────────────────────────────

// Dashboard handles GET /api/wp/metrics/dashboard.
func (h *WPHandler) Dashboard(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	ctx := c.Request.Context()

	var (
		totalCustomers  int
		pendingGroups   int
		totalMerges     int
		mergesThisWeek  int
		newGroups7d     int
		revenueAtRisk   float64
		riskCounts      struct {
			Safe   int `db:"safe"`
			Review int `db:"review"`
			Risky  int `db:"risky"`
		}
		dataQuality struct {
			MissingPhonePct      float64 `db:"missing_phone_pct"`
			UnverifiedEmailPct   float64 `db:"unverified_email_pct"`
			IncompleteAddressPct float64 `db:"incomplete_address_pct"`
		}
		mergesUsed  int
		mergesLimit int
	)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return h.db.GetContext(gCtx, &totalCustomers,
			`SELECT COUNT(*) FROM customer_cache WHERE merchant_id = $1 AND platform = 'wordpress'`,
			merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &pendingGroups,
			`SELECT COUNT(*) FROM duplicate_groups WHERE merchant_id = $1 AND status = 'pending'`,
			merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &totalMerges,
			`SELECT COUNT(*) FROM merge_records WHERE merchant_id = $1`,
			merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &mergesThisWeek,
			`SELECT COUNT(*) FROM merge_records WHERE merchant_id = $1 AND created_at >= NOW() - INTERVAL '7 days'`,
			merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &newGroups7d,
			`SELECT COUNT(*) FROM duplicate_groups WHERE merchant_id = $1 AND created_at >= NOW() - INTERVAL '7 days'`,
			merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &revenueAtRisk,
			`SELECT COALESCE(SUM(impact_score), 0) FROM duplicate_groups WHERE merchant_id = $1 AND status = 'pending'`,
			merchant.ID)
	})

	g.Go(func() error {
		row := h.db.QueryRowContext(gCtx, `
			SELECT
				COUNT(*) FILTER (WHERE risk_level = 'safe')   AS safe,
				COUNT(*) FILTER (WHERE risk_level = 'review') AS review,
				COUNT(*) FILTER (WHERE risk_level = 'risky' OR risk_level IS NULL) AS risky
			FROM duplicate_groups
			WHERE merchant_id = $1 AND status = 'pending'
		`, merchant.ID)
		return row.Scan(&riskCounts.Safe, &riskCounts.Review, &riskCounts.Risky)
	})

	g.Go(func() error {
		row := h.db.QueryRowContext(gCtx, `
			SELECT
				COALESCE(ROUND(COUNT(*) FILTER (WHERE phone = '' OR phone IS NULL)::numeric * 100.0
					/ NULLIF(COUNT(*), 0), 1), 0) AS missing_phone_pct,
				COALESCE(ROUND(COUNT(*) FILTER (WHERE NOT verified_email)::numeric * 100.0
					/ NULLIF(COUNT(*), 0), 1), 0) AS unverified_email_pct,
				COALESCE(ROUND(COUNT(*) FILTER (WHERE address_json IS NULL
					OR address_json::text IN ('{}', 'null', ''))::numeric * 100.0
					/ NULLIF(COUNT(*), 0), 1), 0) AS incomplete_address_pct
			FROM customer_cache
			WHERE merchant_id = $1 AND platform = 'wordpress'
		`, merchant.ID)
		return row.Scan(
			&dataQuality.MissingPhonePct,
			&dataQuality.UnverifiedEmailPct,
			&dataQuality.IncompleteAddressPct,
		)
	})

	// Best-effort settings lookup for plan info.
	g.Go(func() error {
		if s, err := h.settingsRepo.Get(gCtx, merchant.ID); err == nil {
			mergesUsed = s.MergesThisMonth
			switch s.Plan {
			case "basic", "pro":
				mergesLimit = -1
			default:
				mergesLimit = 5
			}
		} else {
			mergesLimit = 5
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		h.log.Error().Err(err).Msg("wp dashboard metrics query")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load metrics"})
		return
	}

	healthScore := 100.0
	duplicateRate := 0.0
	if totalCustomers > 0 {
		healthScore = (1.0 - float64(pendingGroups)/float64(totalCustomers)) * 100
		if healthScore < 0 {
			healthScore = 0
		}
		duplicateRate = float64(pendingGroups) / float64(totalCustomers) * 100
	}

	// Top action groups (risky first).
	type dashGroup struct {
		ID                string  `json:"id"`
		Risk              string  `json:"risk"`
		Confidence        float64 `json:"confidence"`
		CustomersAffected int     `json:"customers_affected"`
		RevenueAtRisk     float64 `json:"revenue_at_risk"`
		Reason            string  `json:"reason"`
	}
	var topGroups []dashGroup
	if rawGroups, err := h.duplicateRepo.ListGroupsByRiskLevels(ctx, merchant.ID, []string{"risky", "review", "safe"}); err == nil {
		limit := 5
		if limit > len(rawGroups) {
			limit = len(rawGroups)
		}
		for _, g := range rawGroups[:limit] {
			risk := "safe"
			if g.RiskLevel != nil {
				risk = *g.RiskLevel
			}
			impact := 0.0
			if g.ImpactScore != nil {
				impact = *g.ImpactScore
			}
			reason := ""
			if len(g.IntelligenceJSON) > 0 {
				if report, err := intelligence.FromRawJSON(g.IntelligenceJSON); err == nil {
					reason = report.Summary
				}
			}
			topGroups = append(topGroups, dashGroup{
				ID:                g.ID.String(),
				Risk:              risk,
				Confidence:        g.ConfidenceScore * 100,
				CustomersAffected: len(g.CustomerIDs),
				RevenueAtRisk:     impact,
				Reason:            reason,
			})
		}
	}
	if topGroups == nil {
		topGroups = []dashGroup{}
	}

	// Recent activity.
	activity := h.loadWPActivity(ctx, merchant.ID.String())

	// Recommendations.
	recommendations := wpRecommendations(pendingGroups, riskCounts.Risky, riskCounts.Review, dataQuality.MissingPhonePct)

	c.JSON(http.StatusOK, gin.H{
		"plan": gin.H{
			"merges_used":  mergesUsed,
			"merges_limit": mergesLimit,
		},
		"metrics": gin.H{
			"health_score":           healthScore,
			"total_customers":        totalCustomers,
			"duplicate_rate":         duplicateRate,
			"revenue_at_risk":        revenueAtRisk,
			"pending_groups":         pendingGroups,
			"total_merges":           totalMerges,
			"merges_this_week":       mergesThisWeek,
			"new_groups_7d":          newGroups7d,
			"missing_phone_pct":      dataQuality.MissingPhonePct,
			"unverified_email_pct":   dataQuality.UnverifiedEmailPct,
			"incomplete_address_pct": dataQuality.IncompleteAddressPct,
			"risk_counts": gin.H{
				"risky":  riskCounts.Risky,
				"review": riskCounts.Review,
				"safe":   riskCounts.Safe,
			},
		},
		"groups":          topGroups,
		"recommendations": recommendations,
		"activity":        activity,
	})
}

func (h *WPHandler) loadWPActivity(ctx context.Context, merchantID string) []gin.H {
	rows, err := h.db.QueryContext(ctx, `
		SELECT 'merge' AS type,
		       primary_customer_id,
		       orders_moved,
		       created_at
		FROM merge_records WHERE merchant_id = $1
		UNION ALL
		SELECT 'scan' AS type,
		       0 AS primary_customer_id,
		       0 AS orders_moved,
		       created_at
		FROM jobs
		WHERE merchant_id = $1
		  AND type = 'detect_duplicates'
		  AND status = 'completed'
		ORDER BY created_at DESC LIMIT 5
	`, merchantID)
	if err != nil {
		return []gin.H{}
	}
	defer rows.Close()

	var items []gin.H
	for rows.Next() {
		var evtType string
		var primaryID int64
		var ordersMoved int
		var ts time.Time
		if err := rows.Scan(&evtType, &primaryID, &ordersMoved, &ts); err != nil {
			continue
		}
		var desc string
		switch evtType {
		case "merge":
			desc = fmt.Sprintf("Merged customer %d — %d order%s reassigned",
				primaryID, ordersMoved, plural(ordersMoved))
		default:
			desc = "Scan completed — duplicate groups updated"
		}
		items = append(items, gin.H{
			"type":        evtType,
			"description": desc,
			"timestamp":   ts.UTC().Format(time.RFC3339),
		})
	}
	if items == nil {
		return []gin.H{}
	}
	return items
}

func wpRecommendations(pendingGroups, riskyCount, reviewCount int, missingPhonePct float64) []string {
	var recs []string
	if riskyCount > 0 {
		recs = append(recs, fmt.Sprintf("Review %d high-risk duplicate group%s before merging", riskyCount, plural(riskyCount)))
	}
	if reviewCount > 0 {
		recs = append(recs, fmt.Sprintf("%d group%s flagged for manual review", reviewCount, plural(reviewCount)))
	}
	if missingPhonePct > 30 {
		recs = append(recs, "Many customers are missing phone numbers — adding phone data improves detection accuracy")
	}
	if pendingGroups > 20 {
		recs = append(recs, "Run a full sync to ensure detection has the latest WooCommerce order data")
	}
	if len(recs) == 0 {
		recs = []string{"No immediate actions required — keep syncing order data regularly"}
	}
	return recs
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ─── Duplicates ──────────────────────────────────────────────────────────────

// ListDuplicates handles GET /api/wp/duplicates.
func (h *WPHandler) ListDuplicates(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	status := c.Query("status")
	search := strings.ToLower(strings.TrimSpace(c.Query("search")))
	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}

	groups, total, err := h.duplicateRepo.ListByMerchant(c.Request.Context(), merchant.ID, status, 0, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("wp list duplicates")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list duplicates"})
		return
	}

	// Collect all customer IDs to enrich with name+email in one query.
	allIDs := make([]int64, 0)
	seen := make(map[int64]struct{})
	for _, g := range groups {
		for _, id := range g.CustomerIDs {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				allIDs = append(allIDs, id)
			}
		}
	}
	type summary struct{ Name, Email string }
	summaryMap := make(map[int64]summary)
	if len(allIDs) > 0 {
		if cached, err := h.customerCacheRepo.FindByExternalIDs(
			c.Request.Context(), merchant.ID, "wordpress", allIDs,
		); err == nil {
			for _, cc := range cached {
				summaryMap[cc.ShopifyCustomerID] = summary{Name: cc.Name, Email: cc.Email}
			}
		}
	}

	type groupItem struct {
		ID                string          `json:"id"`
		Risk              string          `json:"risk"`
		Confidence        float64         `json:"confidence"`
		Status            string          `json:"status"`
		CustomersAffected int             `json:"customers_affected"`
		Summaries         []gin.H `json:"summaries"`
	}

	var items []groupItem
	for _, g := range groups {
		risk := "safe"
		if g.RiskLevel != nil {
			risk = *g.RiskLevel
		}

		summaries := make([]gin.H, 0, len(g.CustomerIDs))
		for _, id := range g.CustomerIDs {
			s := summaryMap[id]
			summaries = append(summaries, gin.H{"name": s.Name, "email": s.Email})
		}

		// Apply search filter in-memory.
		if search != "" {
			match := false
			for _, s := range summaryMap {
				if strings.Contains(strings.ToLower(s.Name), search) ||
					strings.Contains(strings.ToLower(s.Email), search) {
					match = true
					break
				}
			}
			if !match {
				total-- // adjust total for filtered items
				continue
			}
		}

		items = append(items, groupItem{
			ID:                g.ID.String(),
			Risk:              risk,
			Confidence:        g.ConfidenceScore * 100,
			Status:            g.Status,
			CustomersAffected: len(g.CustomerIDs),
			Summaries:         summaries,
		})
	}
	if items == nil {
		items = []groupItem{}
	}

	c.JSON(http.StatusOK, gin.H{
		"total":  total,
		"groups": items,
	})
}

// GetDuplicate handles GET /api/wp/duplicates/:id.
func (h *WPHandler) GetDuplicate(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	group, err := h.duplicateRepo.FindByID(c.Request.Context(), id, merchant.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "duplicate group not found"})
		return
	}

	risk := "safe"
	if group.RiskLevel != nil {
		risk = *group.RiskLevel
	}

	// Enrich customers from WP platform cache.
	type customerItem struct {
		ID           int64      `json:"id"`
		Name         string     `json:"name"`
		Email        string     `json:"email"`
		Phone        string     `json:"phone"`
		Address1     string     `json:"address1"`
		Address2     string     `json:"address2"`
		City         string     `json:"city"`
		State        string     `json:"state"`
		Postcode     string     `json:"postcode"`
		Country      string     `json:"country"`
		OrdersCount  int        `json:"orders_count"`
		TotalSpent   string     `json:"total_spent"`
		AccountState string     `json:"account_state"`
		CreatedAt    *time.Time `json:"created_at"`
		Tags         []string   `json:"tags"`
	}
	var customers []customerItem
	for _, extID := range group.CustomerIDs {
		cached, err := h.customerCacheRepo.FindByExternalID(
			c.Request.Context(), merchant.ID, "wordpress", extID,
		)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				h.log.Warn().Err(err).Int64("ext_id", extID).Msg("wp get duplicate: cache lookup")
			}
			continue
		}
		tags := []string(cached.Tags)
		if tags == nil {
			tags = []string{}
		}
		accountState := cached.State
		if accountState == "" {
			accountState = "active"
		}
		var addr models.OrderAddress
		if len(cached.AddressJSON) > 0 {
			_ = json.Unmarshal(cached.AddressJSON, &addr)
		}
		customers = append(customers, customerItem{
			ID:           extID,
			Name:         cached.Name,
			Email:        cached.Email,
			Phone:        cached.Phone,
			Address1:     addr.Address1,
			Address2:     addr.Address2,
			City:         addr.City,
			State:        addr.State,
			Postcode:     addr.Zip,
			Country:      addr.Country,
			OrdersCount:  cached.OrdersCount,
			TotalSpent:   cached.TotalSpent,
			AccountState: accountState,
			CreatedAt:    cached.ShopifyCreatedAt,
			Tags:         tags,
		})
	}
	if customers == nil {
		customers = []customerItem{}
	}

	// Parse intelligence report.
	var intellResult gin.H
	var recommendedPrimary int64
	if len(group.IntelligenceJSON) > 0 {
		if report, err := intelligence.FromRawJSON(group.IntelligenceJSON); err == nil {
			recommendedPrimary = report.RecommendedPrimary

			conflicts := make([]string, 0)
			for _, ci := range report.Conflicts {
				if ci.Blocking {
					conflicts = append(conflicts, ci.Type)
				}
			}
			reasoning := report.Reasoning
			if reasoning == nil {
				reasoning = []string{}
			}

			breakdown := gin.H{"email": 0, "name": 0, "phone": 0, "address": 0}
			if report.Breakdown != nil {
				breakdown = gin.H{
					"email":   int(report.Breakdown.EmailScore * 100),
					"name":    int(report.Breakdown.NameScore * 100),
					"phone":   int(report.Breakdown.PhoneScore * 100),
					"address": int(report.Breakdown.AddressScore * 100),
				}
			}

			// Split breakdown reasons into positive and negative signal arrays.
			// Negative reasons contain words like "differ" or "different".
			// Risk flags from the report are always negative signals.
			positive := make([]string, 0)
			negative := make([]string, 0)
			if report.Breakdown != nil {
				for _, r := range report.Breakdown.Reasons {
					lower := strings.ToLower(r.Text)
					if strings.Contains(lower, "differ") || strings.Contains(lower, "different") {
						negative = append(negative, r.Text)
					} else {
						positive = append(positive, r.Text)
					}
				}
			}
			for _, flag := range report.RiskFlags {
				negative = append(negative, flag)
			}

			intellResult = gin.H{
				"summary":           report.Summary,
				"confidence_reason": report.Summary,
				"conflicts":         conflicts,
				"breakdown":         breakdown,
				"reasoning":         reasoning,
				"signals": gin.H{
					"positive": positive,
					"negative": negative,
				},
			}
		}
	}
	if intellResult == nil {
		intellResult = gin.H{
			"summary":           "",
			"confidence_reason": "",
			"conflicts":         []string{},
			"breakdown":         gin.H{"email": 0, "name": 0, "phone": 0, "address": 0},
			"reasoning":         []string{},
			"signals":           gin.H{"positive": []string{}, "negative": []string{}},
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":                  group.ID.String(),
		"risk":                risk,
		"confidence":          group.ConfidenceScore * 100,
		"status":              group.Status,
		"recommended_primary": recommendedPrimary,
		"intelligence":        intellResult,
		"customers":           customers,
	})
}

// DismissDuplicate handles POST /api/wp/duplicates/:id/dismiss.
func (h *WPHandler) DismissDuplicate(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if _, err := h.duplicateRepo.FindByID(c.Request.Context(), id, merchant.ID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "duplicate group not found"})
		return
	}

	if err := h.duplicateRepo.DismissGroup(c.Request.Context(), id, ""); err != nil {
		h.log.Error().Err(err).Str("group_id", id.String()).Msg("wp dismiss duplicate")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to dismiss group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"dismissed": true})
}

// ─── Merge ───────────────────────────────────────────────────────────────────

// ExecuteMerge handles POST /api/wp/merge/execute.
// Accepts only a group_id; primary is derived from the intelligence report.
func (h *WPHandler) ExecuteMerge(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		GroupID     string `json:"group_id"     binding:"required"`
		TriggeredBy string `json:"triggered_by"` // WP admin email or display name; optional
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	groupUUID, err := uuid.Parse(req.GroupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	g, err := h.duplicateRepo.FindByID(c.Request.Context(), groupUUID, merchant.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "duplicate group not found"})
			return
		}
		h.log.Error().Err(err).Msg("wp merge execute: load group")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load group"})
		return
	}
	if g.Status == "merged" {
		c.JSON(http.StatusConflict, gin.H{"error": "ALREADY_MERGED", "message": "This group has already been merged"})
		return
	}

	// Derive primary from intelligence report; fall back to first customer ID.
	var primaryID int64
	if len(g.IntelligenceJSON) > 0 {
		if report, err := intelligence.FromRawJSON(g.IntelligenceJSON); err == nil {
			primaryID = report.RecommendedPrimary
		}
	}
	if primaryID == 0 && len(g.CustomerIDs) > 0 {
		primaryID = g.CustomerIDs[0]
	}

	secondaryIDs := make([]int64, 0, len(g.CustomerIDs)-1)
	for _, id := range g.CustomerIDs {
		if id != primaryID {
			secondaryIDs = append(secondaryIDs, id)
		}
	}
	if len(secondaryIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group must have at least 2 customers"})
		return
	}

	performedBy := strings.TrimSpace(req.TriggeredBy)
	if performedBy == "" {
		performedBy = "wp-admin"
	}

	// Load the merchant's current plan so the orchestrator can gate
	// plan-only features (e.g. snapshot creation requires Basic+).
	plan := ""
	if settings, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID); err == nil {
		plan = settings.Plan
	}

	payload := jobs.MergePayload{
		MerchantID:        merchant.ID.String(),
		GroupID:           req.GroupID,
		PrimaryCustomerID: primaryID,
		SecondaryIDs:      secondaryIDs,
		PerformedBy:       performedBy,
		Plan:              plan,
	}

	jobID, err := h.dispatcher.Dispatch(
		c.Request.Context(),
		models.JobTypeMergeCustomersWordPress,
		merchant.ID,
		payload,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("wp merge execute: dispatch")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue merge"})
		return
	}

	if err := h.settingsRepo.IncrementMergeCount(c.Request.Context(), merchant.ID); err != nil {
		h.log.Warn().Err(err).Str("merchant_id", merchant.ID.String()).Msg("wp merge execute: increment merge count")
	}

	c.JSON(http.StatusAccepted, gin.H{
		"accepted": true,
		"job_id":   jobID.String(),
	})
}

// MergeHistory handles GET /api/wp/merge/history.
func (h *WPHandler) MergeHistory(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
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
		h.log.Error().Err(err).Msg("wp merge history")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load history"})
		return
	}

	type historyItem struct {
		PrimaryID    int64     `json:"primary_id"`
		PrimaryName  string    `json:"primary_name"`
		SecondaryIDs []int64   `json:"secondary_ids"`
		OrdersMoved  int       `json:"orders_moved"`
		MergedBy     string    `json:"merged_by"`
		MergedAt     time.Time `json:"merged_at"`
		SnapshotID   *string   `json:"snapshot_id"` // null when plan doesn't include snapshots
	}

	items := make([]historyItem, 0, len(records))
	for _, r := range records {
		// Best-effort name lookup from WP customer cache.
		primaryName := ""
		if cached, err := h.customerCacheRepo.FindByExternalID(
			c.Request.Context(), merchant.ID, "wordpress", r.PrimaryCustomerID,
		); err == nil {
			primaryName = cached.Name
		}

		var snapID *string
		if r.SnapshotID != nil {
			s := r.SnapshotID.String()
			snapID = &s
		}

		items = append(items, historyItem{
			PrimaryID:    r.PrimaryCustomerID,
			PrimaryName:  primaryName,
			SecondaryIDs: []int64(r.SecondaryCustomerIDs),
			OrdersMoved:  r.OrdersMoved,
			MergedBy:     r.PerformedBy,
			MergedAt:     r.CreatedAt,
			SnapshotID:   snapID,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"total":   total,
		"records": items,
	})
}

// ─── Snapshot ────────────────────────────────────────────────────────────────

// GetSnapshot handles GET /api/wp/snapshot/:id.
// Returns the pre-merge customer data captured before the merge executed.
// This is a read-only audit view — WP merges are irreversible (the secondary
// WP user account was deleted by the plugin). The data can be used to manually
// reconstruct the account if needed.
func (h *WPHandler) GetSnapshot(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if settings, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID); err == nil {
		if !billingpkg.IsFeatureEnabled(settings.Plan, billingpkg.FeatureSnapshots) {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "FEATURE_NOT_AVAILABLE",
				"message": "Snapshots are available on the Basic plan and above.",
				"plan":    settings.Plan,
			})
			return
		}
	}

	snapshotID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid snapshot id"})
		return
	}

	snap, data, err := h.snapshotSvc.Get(c.Request.Context(), snapshotID)
	if err != nil {
		if errors.Is(err, snapshotsvc.ErrSnapshotNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
			return
		}
		h.log.Error().Err(err).Str("snapshot_id", snapshotID.String()).Msg("wp snapshot get")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load snapshot"})
		return
	}

	if snap.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	type snapshotCustomer struct {
		UserID      int64    `json:"user_id"`
		Name        string   `json:"name"`
		Email       string   `json:"email"`
		Phone       string   `json:"phone"`
		OrdersCount int      `json:"orders_count"`
		TotalSpent  string   `json:"total_spent"`
		Tags        []string `json:"tags"`
	}

	customers := make([]snapshotCustomer, 0, len(data.Customers))
	for _, sc := range data.Customers {
		name := strings.TrimSpace(sc.FirstName + " " + sc.LastName)
		var tags []string
		for _, t := range strings.Split(sc.Tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
		customers = append(customers, snapshotCustomer{
			UserID:      sc.ID,
			Name:        name,
			Email:       sc.Email,
			Phone:       sc.Phone,
			OrdersCount: sc.OrdersCount,
			TotalSpent:  sc.TotalSpent,
			Tags:        tags,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"snapshot_id": snap.ID.String(),
		"created_at":  snap.CreatedAt,
		"note":        "Pre-merge state. The secondary WP account was permanently deleted — this data is for audit and manual recovery only.",
		"customers":   customers,
	})
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func splitRefreshToken(token string) (uuid.UUID, string, bool) {
	const uuidLen = 36
	if len(token) < uuidLen+2 || token[uuidLen] != ':' {
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(token[:uuidLen])
	if err != nil {
		return uuid.Nil, "", false
	}
	return id, token[uuidLen+1:], true
}

