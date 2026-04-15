package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"merger/backend/internal/middleware"
)

type MetricsHandler struct {
	db  *sqlx.DB
	log zerolog.Logger
}

func NewMetricsHandler(db *sqlx.DB, log zerolog.Logger) *MetricsHandler {
	return &MetricsHandler{db: db, log: log}
}

type activityItem struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type riskBreakdown struct {
	Safe   int `json:"safe"`
	Review int `json:"review"`
	Risky  int `json:"risky"`
}

type businessRiskBreakdown struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
}

type dataQualityMetrics struct {
	MissingPhonePct      float64 `json:"missing_phone_pct"`
	UnverifiedEmailPct   float64 `json:"unverified_email_pct"`
	IncompleteAddressPct float64 `json:"incomplete_address_pct"`
}

type actionItem struct {
	ID                string  `json:"id"                  db:"id"`
	RiskLevel         string  `json:"risk_level"          db:"risk_level"`
	BusinessRiskLevel string  `json:"business_risk_level" db:"business_risk_level"`
	Confidence        float64 `json:"confidence"          db:"confidence_score"`
	CustomerCount     int     `json:"customer_count"      db:"customer_count"`
	ImpactScore       float64 `json:"impact_score"        db:"impact_score"`
}

func (h *MetricsHandler) Dashboard(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	ctx := c.Request.Context()

	type mergeSourceBreakdown struct {
		Behavioral int `json:"behavioral" db:"behavioral"`
		Profile    int `json:"profile"    db:"profile"`
		Mixed      int `json:"mixed"      db:"mixed"`
	}

	var (
		totalCustomers      int
		pendingGroups       int
		highConfidenceCount int
		mergesCompleted     int
		mergesLast7Days     int
		newGroupsLast7Days  int
		totalImpactScore    float64
		totalImpactedOrders int
		riskBreak           riskBreakdown
		bizRiskBreak        businessRiskBreakdown
		dataQuality         dataQualityMetrics
		recentActivity      []activityItem
		actionItems         []actionItem
		mergeSourceCounts   mergeSourceBreakdown
	)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return h.db.GetContext(gCtx, &totalCustomers,
			`SELECT COUNT(*) FROM customer_cache WHERE merchant_id = $1`, merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &pendingGroups,
			`SELECT COUNT(*) FROM duplicate_groups WHERE merchant_id = $1 AND status = 'pending'`, merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &highConfidenceCount,
			`SELECT COUNT(*) FROM duplicate_groups WHERE merchant_id = $1 AND status = 'pending' AND confidence_score >= 0.85`, merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &mergesCompleted,
			`SELECT COUNT(*) FROM merge_records WHERE merchant_id = $1`, merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &mergesLast7Days,
			`SELECT COUNT(*) FROM merge_records WHERE merchant_id = $1 AND created_at >= NOW() - INTERVAL '7 days'`, merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &newGroupsLast7Days,
			`SELECT COUNT(*) FROM duplicate_groups WHERE merchant_id = $1 AND created_at >= NOW() - INTERVAL '7 days'`, merchant.ID)
	})

	// Total revenue at stake across all pending duplicate groups.
	g.Go(func() error {
		return h.db.GetContext(gCtx, &totalImpactScore,
			`SELECT COALESCE(SUM(impact_score), 0) FROM duplicate_groups WHERE merchant_id = $1 AND status = 'pending'`, merchant.ID)
	})

	// Total orders tied up in pending duplicate groups.
	g.Go(func() error {
		return h.db.GetContext(gCtx, &totalImpactedOrders, `
			SELECT COALESCE(SUM(cc.orders_count), 0)
			FROM duplicate_groups dg
			JOIN customer_cache cc
			  ON cc.shopify_customer_id = ANY(dg.customer_ids)
			 AND cc.merchant_id = dg.merchant_id
			WHERE dg.merchant_id = $1 AND dg.status = 'pending'
		`, merchant.ID)
	})

	// Risk level breakdown for pending groups.
	g.Go(func() error {
		row := h.db.QueryRowContext(gCtx, `
			SELECT
				COUNT(*) FILTER (WHERE risk_level = 'safe')   AS safe,
				COUNT(*) FILTER (WHERE risk_level = 'review') AS review,
				COUNT(*) FILTER (WHERE risk_level = 'risky' OR risk_level IS NULL) AS risky
			FROM duplicate_groups
			WHERE merchant_id = $1 AND status = 'pending'
		`, merchant.ID)
		return row.Scan(&riskBreak.Safe, &riskBreak.Review, &riskBreak.Risky)
	})

	// Business risk breakdown for pending groups.
	g.Go(func() error {
		row := h.db.QueryRowContext(gCtx, `
			SELECT
				COUNT(*) FILTER (WHERE business_risk_level = 'high')   AS high,
				COUNT(*) FILTER (WHERE business_risk_level = 'medium') AS medium
			FROM duplicate_groups
			WHERE merchant_id = $1 AND status = 'pending'
		`, merchant.ID)
		return row.Scan(&bizRiskBreak.High, &bizRiskBreak.Medium)
	})

	// Customer data quality metrics.
	g.Go(func() error {
		row := h.db.QueryRowContext(gCtx, `
			SELECT
				ROUND(COUNT(*) FILTER (WHERE phone = '' OR phone IS NULL)::numeric * 100.0
					/ NULLIF(COUNT(*), 0), 1) AS missing_phone_pct,
				ROUND(COUNT(*) FILTER (WHERE NOT verified_email)::numeric * 100.0
					/ NULLIF(COUNT(*), 0), 1) AS unverified_email_pct,
				ROUND(COUNT(*) FILTER (WHERE address_json IS NULL
					OR address_json::text IN ('{}', 'null', ''))::numeric * 100.0
					/ NULLIF(COUNT(*), 0), 1) AS incomplete_address_pct
			FROM customer_cache
			WHERE merchant_id = $1
		`, merchant.ID)
		return row.Scan(
			&dataQuality.MissingPhonePct,
			&dataQuality.UnverifiedEmailPct,
			&dataQuality.IncompleteAddressPct,
		)
	})

	g.Go(func() error {
		return h.loadRecentActivity(gCtx, merchant.ID.String(), &recentActivity)
	})

	// Top-priority action items: risky first, then high business risk, then review.
	g.Go(func() error {
		return h.db.SelectContext(gCtx, &actionItems, `
			SELECT
				id::text,
				COALESCE(risk_level, 'risky')     AS risk_level,
				COALESCE(business_risk_level, '')  AS business_risk_level,
				confidence_score,
				array_length(customer_ids, 1)      AS customer_count,
				COALESCE(impact_score, 0)           AS impact_score
			FROM duplicate_groups
			WHERE merchant_id = $1 AND status = 'pending'
			ORDER BY
				CASE COALESCE(risk_level, 'risky')
					WHEN 'risky'  THEN 1
					WHEN 'review' THEN 2
					ELSE               3
				END,
				CASE COALESCE(business_risk_level, '')
					WHEN 'high'   THEN 1
					WHEN 'medium' THEN 2
					ELSE               3
				END,
				confidence_score DESC
			LIMIT 5
		`, merchant.ID)
	})

	g.Go(func() error {
		return h.db.GetContext(gCtx, &mergeSourceCounts, `
			SELECT
				COUNT(*) FILTER (WHERE confidence_source = 'behavioral') AS behavioral,
				COUNT(*) FILTER (WHERE confidence_source = 'profile')    AS profile,
				COUNT(*) FILTER (WHERE confidence_source = 'mixed')      AS mixed
			FROM merge_records
			WHERE merchant_id = $1`, merchant.ID)
	})

	if err := g.Wait(); err != nil {
		h.log.Error().Err(err).Msg("dashboard metrics query")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load metrics"})
		return
	}

	if actionItems == nil {
		actionItems = []actionItem{}
	}
	if recentActivity == nil {
		recentActivity = []activityItem{}
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

	c.JSON(http.StatusOK, gin.H{
		// Core metrics
		"health_score":          healthScore,
		"duplicate_rate":        duplicateRate,
		"total_customers":       totalCustomers,
		"duplicate_count":       pendingGroups,
		"high_confidence_count": highConfidenceCount,
		"merges_completed":      mergesCompleted,
		"recent_activity":       recentActivity,
		// Risk intelligence
		"risk_breakdown":          riskBreak,
		"business_risk_breakdown": bizRiskBreak,
		// Business impact
		"total_impact_score":    totalImpactScore,
		"total_impacted_orders": totalImpactedOrders,
		// Data quality
		"data_quality": dataQuality,
		// 7-day trends
		"merges_last_7_days":     mergesLast7Days,
		"new_groups_last_7_days": newGroupsLast7Days,
		// Action center
		"action_items": actionItems,
		// Merge source breakdown
		"merge_source_counts": mergeSourceCounts,
	})
}

func (h *MetricsHandler) loadRecentActivity(ctx context.Context, merchantID string, out *[]activityItem) error {
	rows, err := h.db.QueryContext(ctx, `
		SELECT id::text, 'merge' as type,
		       'Merged customer ' || primary_customer_id::text as description,
		       created_at
		FROM merge_records WHERE merchant_id = $1
		UNION ALL
		SELECT id::text, 'scan' as type,
		       'Duplicate scan completed' as description,
		       created_at
		FROM jobs WHERE merchant_id = $1 AND type = 'detect_duplicates'
		  AND status = 'completed'
		ORDER BY created_at DESC LIMIT 5
	`, merchantID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var item activityItem
		if err := rows.Scan(&item.ID, &item.Type, &item.Description, &item.CreatedAt); err != nil {
			return err
		}
		*out = append(*out, item)
	}
	return rows.Err()
}
