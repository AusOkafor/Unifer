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
	syncsvc "merger/backend/internal/services/sync"
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

	// Migrations require a direct connection (not pgbouncer) because the
	// migration protocol uses advisory locks that pgbouncer doesn't support.
	// Fall back to DatabaseURL when DIRECT_URL is not set (local dev).
	migrationURL := cfg.DirectURL
	if migrationURL == "" {
		migrationURL = cfg.DatabaseURL
	}
	if err := db.RunMigrations(migrationURL); err != nil {
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
	webhookIdempotency := queue.NewWebhookIdempotencyStore(redisClient)

	// --- Job dispatcher (used by handlers; processor wired below) ---
	dispatcher := jobs.NewDispatcher(jobRepo, q, log)

	// --- Shopify OAuth ---
	oauthCfg := &shopifyauth.OAuthConfig{
		APIKey:    cfg.ShopifyAPIKey,
		APISecret: cfg.ShopifyAPISecret,
		AppURL:    cfg.AppURL,
	}

	// --- Core services ---
	snapshotSvc := snapshotsvc.NewService(snapshotRepo)
	validator := mergesvc.NewValidator()
	orchestrator := mergesvc.NewOrchestrator(
		validator, snapshotSvc,
		mergeRepo, duplicateRepo,
		customerCacheRepo, merchantRepo, encryptor, log,
	)
	analyzer := intelligence.NewAnalyzer()
	detector := identity.NewDetector(customerCacheRepo, duplicateRepo, settingsRepo, analyzer, log)
	syncService := syncsvc.NewService(merchantRepo, customerCacheRepo, encryptor, log)
	processor := jobs.NewProcessor(detector, orchestrator, snapshotSvc, syncService, jobRepo, dispatcher, log)
	worker := jobs.NewWorker(q, processor, jobRepo, 3, log)

	// --- Handlers ---
	h := &server.Handlers{
		Auth:      handlers.NewAuthHandler(oauthCfg, merchantRepo, encryptor, cfg.JWTSecret, cfg.FrontendURL, log),
		Duplicate: handlers.NewDuplicateHandler(duplicateRepo, customerCacheRepo, settingsRepo, log),
		Merge:     handlers.NewMergeHandler(mergeRepo, duplicateRepo, customerCacheRepo, dispatcher, log),
		Job:       handlers.NewJobHandler(jobRepo, log),
		Snapshot:  handlers.NewSnapshotHandler(snapshotRepo, snapshotSvc, dispatcher, log),
		Metrics:   handlers.NewMetricsHandler(sqlDB, log),
		Settings:  handlers.NewSettingsHandler(settingsRepo, log),
		Scan:      handlers.NewScanHandler(dispatcher, log),
		Webhook: handlers.NewWebhookHandler(
			cfg.ShopifyWebhookSecret,
			merchantRepo,
			customerCacheRepo,
			settingsRepo,
			dispatcher,
			webhookIdempotency,
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

	// Register Shopify webhooks for all existing merchants on startup.
	// This ensures webhooks are always registered even after redeployments,
	// and fixes merchants whose install happened before RegisterAll was wired up.
	go func() {
		ctx := context.Background()
		merchants, err := merchantRepo.ListAll(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("startup: failed to list merchants for webhook registration")
			return
		}
		for _, m := range merchants {
			token, err := encryptor.Decrypt(m.AccessTokenEnc)
			if err != nil {
				log.Warn().Err(err).Str("shop", m.ShopDomain).Msg("startup: decrypt token failed")
				continue
			}
			client := shopifysvc.NewClient(m.ShopDomain, token, log)
			whSvc := shopifysvc.NewWebhookService(client)
			if err := whSvc.RegisterAll(ctx, cfg.AppURL); err != nil {
				log.Warn().Err(err).Str("shop", m.ShopDomain).Msg("startup: webhook registration failed")
			} else {
				log.Info().Str("shop", m.ShopDomain).Msg("startup: webhooks registered")
			}
		}
	}()

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
