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

func (h *MetricsHandler) Dashboard(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	ctx := c.Request.Context()

	var (
		totalCustomers  int
		pendingGroups   int
		mergesCompleted int
		recentActivity  []activityItem
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
		return h.db.GetContext(gCtx, &mergesCompleted,
			`SELECT COUNT(*) FROM merge_records WHERE merchant_id = $1`, merchant.ID)
	})

	g.Go(func() error {
		return h.loadRecentActivity(gCtx, merchant.ID.String(), &recentActivity)
	})

	if err := g.Wait(); err != nil {
		h.log.Error().Err(err).Msg("dashboard metrics query")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load metrics"})
		return
	}

	healthScore := 100.0
	if totalCustomers > 0 {
		healthScore = (1.0 - float64(pendingGroups)/float64(totalCustomers)) * 100
		if healthScore < 0 {
			healthScore = 0
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"health_score":      healthScore,
		"total_customers":   totalCustomers,
		"duplicate_count":   pendingGroups,
		"merges_completed":  mergesCompleted,
		"recent_activity":   recentActivity,
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
		       'Duplicate scan — type: ' || type as description,
		       created_at
		FROM jobs WHERE merchant_id = $1 AND type = 'detect_duplicates'
		  AND status = 'completed'
		ORDER BY created_at DESC LIMIT 10
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
