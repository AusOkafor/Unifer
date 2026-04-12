package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"merger/backend/internal/api/handlers"
	"merger/backend/internal/config"
	"merger/backend/internal/db"
	"merger/backend/internal/queue"
	"merger/backend/internal/repository"
	"merger/backend/internal/server"
	"merger/backend/internal/services/identity"
	"merger/backend/internal/services/intelligence"
	"merger/backend/internal/services/jobs"
	mergesvc "merger/backend/internal/services/merge"
	snapshotsvc "merger/backend/internal/services/snapshot"
	shopifysvc "merger/backend/internal/services/shopify"
	"merger/backend/internal/utils"
	"merger/backend/pkg/shopifyauth"
)

func main() {
	log := utils.NewLogger("development")

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	log = utils.NewLogger(cfg.Environment)
	log.Info().Str("env", cfg.Environment).Msg("starting Customer Harmony backend")

	// --- Database ---
	sqlDB, err := db.NewPostgres(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to postgres")
	}
	defer sqlDB.Close()

	if err := db.RunMigrations(sqlDB); err != nil {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}
	log.Info().Msg("postgres + migrations ready")

	// --- Redis ---
	redisClient, err := queue.NewRedisClient(cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to redis")
	}
	defer redisClient.Close()
	log.Info().Msg("redis connected")

	// --- Crypto ---
	encryptor, err := utils.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid encryption key")
	}

	// --- Repositories ---
	merchantRepo := repository.NewMerchantRepo(sqlDB)
	customerCacheRepo := repository.NewCustomerCacheRepo(sqlDB)
	duplicateRepo := repository.NewDuplicateRepo(sqlDB)
	mergeRepo := repository.NewMergeRepo(sqlDB)
	snapshotRepo := repository.NewSnapshotRepo(sqlDB)
	jobRepo := repository.NewJobRepo(sqlDB)
	settingsRepo := repository.NewSettingsRepo(sqlDB)

	// --- Queue ---
	q := queue.New(redisClient)

	// --- Job dispatcher (used by handlers; processor wired below) ---
	dispatcher := jobs.NewDispatcher(jobRepo, q, log)

	// --- Shopify OAuth ---
	oauthCfg := &shopifyauth.OAuthConfig{
		APIKey:    cfg.ShopifyAPIKey,
		APISecret: cfg.ShopifyAPISecret,
		AppURL:    cfg.AppURL,
	}

	// Build a placeholder Shopify client for wiring services.
	// In production, job processor creates per-merchant clients using decrypted tokens.
	sampleShopifyClient := shopifysvc.NewClient("placeholder.myshopify.com", "", log)
	customerSvc := shopifysvc.NewCustomerService(sampleShopifyClient)
	orderSvc := shopifysvc.NewOrderService(sampleShopifyClient)

	// --- Core services ---
	snapshotSvc := snapshotsvc.NewService(snapshotRepo, customerSvc, orderSvc)
	validator := mergesvc.NewValidator()
	executor := mergesvc.NewExecutor(customerSvc)
	orchestrator := mergesvc.NewOrchestrator(
		validator, executor, snapshotSvc,
		mergeRepo, duplicateRepo, customerSvc, log,
	)
	analyzer := intelligence.NewAnalyzer()
	detector := identity.NewDetector(customerCacheRepo, duplicateRepo, analyzer, log)
	processor := jobs.NewProcessor(detector, orchestrator, snapshotSvc, jobRepo, log)
	worker := jobs.NewWorker(q, processor, jobRepo, 3, log)

	// --- Handlers ---
	h := &server.Handlers{
		Auth: handlers.NewAuthHandler(oauthCfg, merchantRepo, encryptor, cfg.JWTSecret, cfg.FrontendURL, log),
		Duplicate: handlers.NewDuplicateHandler(duplicateRepo, customerCacheRepo, log),
		Merge:     handlers.NewMergeHandler(mergeRepo, dispatcher, log),
		Job:       handlers.NewJobHandler(jobRepo, log),
		Snapshot:  handlers.NewSnapshotHandler(snapshotRepo, dispatcher, log),
		Metrics:   handlers.NewMetricsHandler(sqlDB, log),
		Settings:  handlers.NewSettingsHandler(settingsRepo, log),
		Webhook:   handlers.NewWebhookHandler(
			cfg.ShopifyWebhookSecret,
			merchantRepo,
			customerCacheRepo,
			dispatcher,
			log,
		),
	}

	// --- Server ---
	srv := server.New(cfg, log, merchantRepo, h)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      srv.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start worker in background
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	go worker.Start(workerCtx)

	// Start server
	go func() {
		log.Info().Str("port", cfg.Port).Msg("server listening")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down...")
	cancelWorker()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("server shutdown error")
	}
	log.Info().Msg("server stopped")
}
