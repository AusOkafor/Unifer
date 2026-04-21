package jobs

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	billingpkg "merger/backend/internal/services/billing"
)

// Scheduler runs daily detection for every merchant that has
// scan_frequency = "daily" and auto_detect = true, at the hour each
// merchant has chosen (scan_hour, 0–23 UTC).
//
// It ticks every hour on the hour. On startup it waits until the next
// top-of-hour so merchants always get a consistent run time regardless
// of when the server deploys.
type Scheduler struct {
	merchantRepo repository.MerchantRepository
	settingsRepo repository.SettingsRepository
	notifRepo    repository.NotificationRepository
	dispatcher   *Dispatcher
	log          zerolog.Logger
}

func NewScheduler(
	merchantRepo repository.MerchantRepository,
	settingsRepo repository.SettingsRepository,
	notifRepo repository.NotificationRepository,
	dispatcher *Dispatcher,
	log zerolog.Logger,
) *Scheduler {
	return &Scheduler{
		merchantRepo: merchantRepo,
		settingsRepo: settingsRepo,
		notifRepo:    notifRepo,
		dispatcher:   dispatcher,
		log:          log,
	}
}

// Start blocks until ctx is cancelled, running merchant scans at the top
// of each UTC hour and dispatching those whose scan_hour matches.
func (s *Scheduler) Start(ctx context.Context) {
	wait := durationUntilNextHour()
	s.log.Info().
		Str("next_run", time.Now().UTC().Add(wait).Format(time.RFC3339)).
		Msg("daily scheduler: waiting for next hour boundary")

	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}

	s.scheduledRun(ctx)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info().Msg("daily scheduler: stopped")
			return
		case <-ticker.C:
			s.scheduledRun(ctx)
		}
	}
}

// RunNow immediately executes a daily scan for all eligible merchants.
// Used by the test endpoint so you can verify the scheduler logic without
// waiting for the next scheduled hour.
func (s *Scheduler) RunNow(ctx context.Context) {
	s.runDailyScan(ctx)
}

func (s *Scheduler) runDailyScan(ctx context.Context) {
	s.log.Info().Msg("daily scheduler: starting (run-now — all eligible merchants)")

	merchants, err := s.merchantRepo.ListAll(ctx)
	if err != nil {
		s.log.Error().Err(err).Msg("daily scheduler: list merchants failed")
		return
	}

	dispatched := 0
	for _, m := range merchants {
		settings, err := s.settingsRepo.Get(ctx, m.ID)
		if err != nil {
			s.log.Warn().Err(err).Str("shop", m.ShopDomain).Msg("daily scheduler: load settings failed")
			continue
		}
		if !settings.AutoDetect || settings.ScanFrequency != "daily" {
			continue
		}
		if !billingpkg.IsFeatureEnabled(settings.Plan, billingpkg.FeatureAutoDetect) {
			continue
		}
		// When triggered via RunNow (test endpoint), currentHour is checked
		// against scan_hour only for the real scheduled path. RunNow always
		// dispatches all eligible merchants regardless of hour.
		if _, err := s.dispatcher.Dispatch(
			ctx,
			models.JobTypeDetectDuplicates,
			m.ID,
			map[string]string{"merchant_id": m.ID.String()},
		); err != nil {
			s.log.Warn().Err(err).Str("shop", m.ShopDomain).Msg("daily scheduler: dispatch failed")
			continue
		}
		dispatched++
		s.log.Info().Str("shop", m.ShopDomain).Int("scan_hour", settings.ScanHour).Msg("daily scheduler: queued detection")
	}

	s.log.Info().Int("dispatched", dispatched).Int("total", len(merchants)).Msg("daily scheduler: done")
}

// scheduledRun is called by the hourly ticker — only dispatches merchants
// whose scan_hour matches the current UTC hour.
func (s *Scheduler) scheduledRun(ctx context.Context) {
	currentHour := time.Now().UTC().Hour()
	s.log.Info().Int("hour_utc", currentHour).Msg("daily scheduler: starting")

	// At midnight UTC, purge notifications older than 30 days.
	if currentHour == 0 {
		if err := s.notifRepo.DeleteOld(ctx); err != nil {
			s.log.Warn().Err(err).Msg("daily scheduler: notification cleanup failed")
		} else {
			s.log.Info().Msg("daily scheduler: old notifications purged")
		}
	}

	merchants, err := s.merchantRepo.ListAll(ctx)
	if err != nil {
		s.log.Error().Err(err).Msg("daily scheduler: list merchants failed")
		return
	}

	dispatched := 0
	for _, m := range merchants {
		settings, err := s.settingsRepo.Get(ctx, m.ID)
		if err != nil {
			s.log.Warn().Err(err).Str("shop", m.ShopDomain).Msg("daily scheduler: load settings failed")
			continue
		}
		if !settings.AutoDetect || settings.ScanFrequency != "daily" {
			continue
		}
		if !billingpkg.IsFeatureEnabled(settings.Plan, billingpkg.FeatureAutoDetect) {
			continue
		}
		if settings.ScanHour != currentHour {
			continue
		}
		if _, err := s.dispatcher.Dispatch(
			ctx,
			models.JobTypeDetectDuplicates,
			m.ID,
			map[string]string{"merchant_id": m.ID.String()},
		); err != nil {
			s.log.Warn().Err(err).Str("shop", m.ShopDomain).Msg("daily scheduler: dispatch failed")
			continue
		}
		dispatched++
		s.log.Info().Str("shop", m.ShopDomain).Int("scan_hour", settings.ScanHour).Msg("daily scheduler: queued detection")
	}

	s.log.Info().Int("dispatched", dispatched).Int("total", len(merchants)).Msg("daily scheduler: done")
}

// durationUntilNextHour returns the duration from now until the next top-of-hour UTC.
func durationUntilNextHour() time.Duration {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+1, 0, 0, 0, time.UTC)
	return next.Sub(now)
}

// durationUntilUTCHour returns the duration from now until the next occurrence
// of the given hour (0–23) in UTC. Kept for reference.
func durationUntilUTCHour(hour int) time.Duration {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}
