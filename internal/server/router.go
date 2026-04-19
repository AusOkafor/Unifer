package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"merger/backend/internal/api/handlers"
	"merger/backend/internal/config"
	"merger/backend/internal/middleware"
	"merger/backend/internal/repository"
)

// Handlers holds all the application handlers.
type Handlers struct {
	Auth         *handlers.AuthHandler
	Billing      *handlers.BillingHandler
	Duplicate    *handlers.DuplicateHandler
	Merge        *handlers.MergeHandler
	Job          *handlers.JobHandler
	Snapshot     *handlers.SnapshotHandler
	Metrics      *handlers.MetricsHandler
	Settings     *handlers.SettingsHandler
	Scan         *handlers.ScanHandler
	Webhook      *handlers.WebhookHandler
	Notification *handlers.NotificationHandler
}

type Server struct {
	engine       *gin.Engine
	cfg          *config.Config
	log          zerolog.Logger
	h            *Handlers
	merchantRepo repository.MerchantRepository
}

func New(cfg *config.Config, log zerolog.Logger, merchantRepo repository.MerchantRepository, h *Handlers) *Server {
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	s := &Server{
		engine:       gin.New(),
		cfg:          cfg,
		log:          log,
		h:            h,
		merchantRepo: merchantRepo,
	}

	s.engine.Use(Recovery(s.log))
	s.engine.Use(CORS(s.cfg.FrontendURL))
	s.engine.Use(RequestLogger(s.log))

	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "1.0.0"})
	})

	// Auth routes
	authGroup := s.engine.Group("/auth")
	if s.h.Auth != nil {
		authGroup.GET("/shopify", s.h.Auth.HandleInstall)
		authGroup.GET("/shopify/callback", s.h.Auth.HandleCallback)
	} else {
		authGroup.GET("/shopify", s.stub("auth: install"))
		authGroup.GET("/shopify/callback", s.stub("auth: callback"))
	}

	// Billing callback — no JWT (Shopify redirects the merchant here after approval).
	if s.h.Billing != nil {
		s.engine.GET("/api/billing/callback", s.h.Billing.Callback)
		s.engine.GET("/api/billing/plans", s.h.Billing.Plans)
	} else {
		s.engine.GET("/api/billing/callback", s.stub("billing: callback"))
		s.engine.GET("/api/billing/plans", s.stub("billing: plans"))
	}

	// Webhook — HMAC-verified, no JWT
	if s.h.Webhook != nil {
		s.engine.POST("/api/webhooks/shopify", s.h.Webhook.Handle)
	} else {
		s.engine.POST("/api/webhooks/shopify", s.stub("webhooks: shopify"))
	}

	// Protected API routes
	api := s.engine.Group("/api")
	api.Use(middleware.AuthRequired(s.cfg.ShopifyAPISecret, s.cfg.ShopifyAPIKey, s.merchantRepo))

	if s.h.Duplicate != nil {
		api.GET("/duplicates", s.h.Duplicate.List)
		api.GET("/duplicates/:id", s.h.Duplicate.Get)
		api.POST("/duplicates/:id/dismiss", s.h.Duplicate.Dismiss)
	} else {
		api.GET("/duplicates", s.stub("duplicates: list"))
		api.GET("/duplicates/:id", s.stub("duplicates: get"))
		api.POST("/duplicates/:id/dismiss", s.stub("duplicates: dismiss"))
	}

	if s.h.Merge != nil {
		api.POST("/merge/execute", s.h.Merge.Execute)
		api.POST("/merge/validate", s.h.Merge.ValidateProfile)
		api.GET("/merge/history", s.h.Merge.History)
		api.GET("/merge/bulk-preview", s.h.Merge.BulkPreview)
		api.POST("/merge/safe-bulk", s.h.Merge.SafeBulkMerge)
	} else {
		api.POST("/merge/execute", s.stub("merge: execute"))
		api.POST("/merge/validate", s.stub("merge: validate"))
		api.GET("/merge/history", s.stub("merge: history"))
		api.GET("/merge/bulk-preview", s.stub("merge: bulk-preview"))
		api.POST("/merge/safe-bulk", s.stub("merge: safe-bulk"))
	}

	if s.h.Job != nil {
		api.GET("/jobs/:id", s.h.Job.Status)
	} else {
		api.GET("/jobs/:id", s.stub("jobs: status"))
	}

	if s.h.Snapshot != nil {
		api.GET("/snapshot/:id", s.h.Snapshot.Get)
		api.POST("/snapshot/restore/:id", s.h.Snapshot.Restore)
	} else {
		api.GET("/snapshot/:id", s.stub("snapshot: get"))
		api.POST("/snapshot/restore/:id", s.stub("snapshot: restore"))
	}

	if s.h.Metrics != nil {
		api.GET("/metrics/dashboard", s.h.Metrics.Dashboard)
	} else {
		api.GET("/metrics/dashboard", s.stub("metrics: dashboard"))
	}

	if s.h.Scan != nil {
		api.POST("/scan", s.h.Scan.Trigger)
		api.POST("/scan/daily", s.h.Scan.TriggerDailyScan)
	} else {
		api.POST("/scan", s.stub("scan: trigger"))
		api.POST("/scan/daily", s.stub("scan: trigger-daily"))
	}

	if s.h.Settings != nil {
		api.GET("/settings", s.h.Settings.Get)
		api.PUT("/settings", s.h.Settings.Update)
	} else {
		api.GET("/settings", s.stub("settings: get"))
		api.PUT("/settings", s.stub("settings: update"))
	}

	// Billing — protected routes (require merchant session token).
	if s.h.Billing != nil {
		api.POST("/billing/subscribe", s.h.Billing.Subscribe)
		api.GET("/billing/current", s.h.Billing.CurrentPlan)
	} else {
		api.POST("/billing/subscribe", s.stub("billing: subscribe"))
		api.GET("/billing/current", s.stub("billing: current"))
	}

	if s.h.Notification != nil {
		api.GET("/notifications", s.h.Notification.List)
		api.POST("/notifications/read-all", s.h.Notification.MarkAllRead)
		api.PATCH("/notifications/:id/read", s.h.Notification.MarkRead)
	} else {
		api.GET("/notifications", s.stub("notifications: list"))
		api.POST("/notifications/read-all", s.stub("notifications: read-all"))
		api.PATCH("/notifications/:id/read", s.stub("notifications: mark-read"))
	}
}

func (s *Server) stub(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{"message": name + " — not yet implemented"})
	}
}

func (s *Server) Run() error {
	return s.engine.Run(":" + s.cfg.Port)
}

func (s *Server) Handler() http.Handler {
	return s.engine
}
