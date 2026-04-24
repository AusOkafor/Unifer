package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

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
	notifsvc "merger/backend/internal/services/notification"
	snapshotsvc "merger/backend/internal/services/snapshot"
	shopifysvc "merger/backend/internal/services/shopify"
	syncsvc "merger/backend/internal/services/sync"
	wpsvc "merger/backend/internal/services/wordpress"
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
	log.Info().Str("env", cfg.Environment).Msg("starting MergeIQ backend")

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
	notifRepo := repository.NewNotificationRepo(sqlDB)
	wpRefreshTokenRepo := repository.NewWPRefreshTokenRepo(sqlDB)

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
	notificationSvc := notifsvc.NewService(notifRepo, settingsRepo, sqlDB, log)

	// --- WordPress services ---
	wpSyncSvc := wpsvc.NewSyncService(customerCacheRepo, log)
	wpOrchestrator := mergesvc.NewOrchestrator(
		wpsvc.NewWPValidator(), snapshotSvc,
		mergeRepo, duplicateRepo,
		customerCacheRepo, merchantRepo, encryptor, log,
	)
	wpOrchestrator.SetExecutorFactory(func(domain, token string, l zerolog.Logger) mergesvc.MergeExecutor {
		return wpsvc.NewExecutor(wpsvc.NewClient(domain, token, l), customerCacheRepo, merchantRepo, domain, l)
	})

	// Warn if WP_JWT_SECRET is not set (non-fatal for Shopify-only deploys).
	if err := cfg.WPJWTSecretWarning(); err != nil {
		log.Warn().Msg(err.Error())
	}

	processor := jobs.NewProcessor(detector, orchestrator, snapshotSvc, syncService, jobRepo, dispatcher, notificationSvc, log)
	processor.SetWPOrchestrator(wpOrchestrator)
	worker := jobs.NewWorker(q, processor, jobRepo, 3, log)
	scheduler := jobs.NewScheduler(merchantRepo, settingsRepo, notifRepo, dispatcher, log)

	// --- WordPress handler ---
	wpHandler := handlers.NewWPHandler(
		merchantRepo, wpRefreshTokenRepo, wpSyncSvc,
		dispatcher, encryptor,
		duplicateRepo, customerCacheRepo, mergeRepo, settingsRepo, sqlDB,
		cfg.WPJWTSecret, log,
	)

	// --- Handlers ---
	h := &server.Handlers{
		Auth: handlers.NewAuthHandler(oauthCfg, merchantRepo, encryptor, cfg.ShopifyAPIKey, log),
		Billing: handlers.NewBillingHandler(
			settingsRepo, merchantRepo, customerCacheRepo, encryptor,
			cfg.AppURL, cfg.ShopifyAPIKey,
			cfg.Environment != "production", // test mode outside production
			log,
		),
		Duplicate: handlers.NewDuplicateHandler(duplicateRepo, customerCacheRepo, settingsRepo, log),
		Merge:     handlers.NewMergeHandler(mergeRepo, snapshotRepo, duplicateRepo, customerCacheRepo, settingsRepo, dispatcher, log),
		Job:       handlers.NewJobHandler(jobRepo, log),
		Snapshot:  handlers.NewSnapshotHandler(snapshotRepo, snapshotSvc, settingsRepo, dispatcher, log),
		Metrics:   handlers.NewMetricsHandler(sqlDB, log),
		Settings:  handlers.NewSettingsHandler(settingsRepo, log),
		Scan:      handlers.NewScanHandlerWithScheduler(dispatcher, scheduler, log),
		Webhook: handlers.NewWebhookHandler(
			cfg.ShopifyWebhookSecret,
			merchantRepo,
			customerCacheRepo,
			settingsRepo,
			dispatcher,
			webhookIdempotency,
			log,
		),
		Notification: handlers.NewNotificationHandler(notifRepo, log),
		WP:           wpHandler,
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
			if m.Platform != "" && m.Platform != "shopify" {
				continue // webhook registration is Shopify-only
			}
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

	// Start worker and daily scheduler in background
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	go worker.Start(workerCtx)
	go scheduler.Start(workerCtx)

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
