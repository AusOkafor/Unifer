package jobs

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
)

// Scheduler runs daily detection for every merchant that has
// scan_frequency = "daily" and auto_detect = true.
//
// It fires at 3 AM UTC each day. On startup it waits until the next
// 3 AM window rather than using a fixed 24h ticker from the start time,
// so merchants always get a consistent nightly scan regardless of deploys.
type Scheduler struct {
	merchantRepo repository.MerchantRepository
	settingsRepo repository.SettingsRepository
	dispatcher   *Dispatcher
	log          zerolog.Logger
}

func NewScheduler(
	merchantRepo repository.MerchantRepository,
	settingsRepo repository.SettingsRepository,
	dispatcher *Dispatcher,
	log zerolog.Logger,
) *Scheduler {
	return &Scheduler{
		merchantRepo: merchantRepo,
		settingsRepo: settingsRepo,
		dispatcher:   dispatcher,
		log:          log,
	}
}

// Start blocks until ctx is cancelled, running daily scans at 3 AM UTC.
func (s *Scheduler) Start(ctx context.Context) {
	wait := durationUntilUTCHour(3)
	s.log.Info().
		Str("next_run", time.Now().UTC().Add(wait).Format(time.RFC3339)).
		Msg("daily scheduler: waiting for next 3 AM UTC window")

	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}

	s.runDailyScan(ctx)

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info().Msg("daily scheduler: stopped")
			return
		case <-ticker.C:
			s.runDailyScan(ctx)
		}
	}
}

func (s *Scheduler) runDailyScan(ctx context.Context) {
	s.log.Info().Msg("daily scanner: starting")

	merchants, err := s.merchantRepo.ListAll(ctx)
	if err != nil {
		s.log.Error().Err(err).Msg("daily scanner: list merchants failed")
		return
	}

	dispatched := 0
	for _, m := range merchants {
		settings, err := s.settingsRepo.Get(ctx, m.ID)
		if err != nil {
			s.log.Warn().Err(err).Str("shop", m.ShopDomain).Msg("daily scanner: load settings failed")
			continue
		}
		if !settings.AutoDetect || settings.ScanFrequency != "daily" {
			continue
		}
		if _, err := s.dispatcher.Dispatch(
			ctx,
			models.JobTypeDetectDuplicates,
			m.ID,
			map[string]string{"merchant_id": m.ID.String()},
		); err != nil {
			s.log.Warn().Err(err).Str("shop", m.ShopDomain).Msg("daily scanner: dispatch failed")
			continue
		}
		dispatched++
		s.log.Info().Str("shop", m.ShopDomain).Msg("daily scanner: queued detection")
	}

	s.log.Info().Int("dispatched", dispatched).Int("total", len(merchants)).Msg("daily scanner: done")
}

// durationUntilUTCHour returns the duration from now until the next occurrence
// of the given hour (0–23) in UTC. Always returns a positive duration.
func durationUntilUTCHour(hour int) time.Duration {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}
