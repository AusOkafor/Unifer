// Package notification creates in-app notifications after key events
// (detection complete, merge complete, merge failed). It gates each
// notification type behind the merchant's per-toggle settings so
// merchants only see what they opted into.
package notification

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
)

// Service creates notifications for detection and merge events.
type Service struct {
	notifRepo    repository.NotificationRepository
	settingsRepo repository.SettingsRepository
	db           rawQuerier
	log          zerolog.Logger
}

// rawQuerier is a minimal interface for the one raw count query we need.
// sqlx.DB satisfies this.
type rawQuerier interface {
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
}

func NewService(
	notifRepo repository.NotificationRepository,
	settingsRepo repository.SettingsRepository,
	db rawQuerier,
	log zerolog.Logger,
) *Service {
	return &Service{
		notifRepo:    notifRepo,
		settingsRepo: settingsRepo,
		db:           db,
		log:          log,
	}
}

// OnDetectComplete is called after a successful detect_duplicates job.
// Creates up to two notifications: one for new duplicates and one for high-risk
// groups, each gated behind the merchant's notification settings.
func (s *Service) OnDetectComplete(ctx context.Context, merchantID uuid.UUID) {
	settings, err := s.settingsRepo.Get(ctx, merchantID)
	if err != nil || !settings.NotificationsEnabled {
		return
	}

	if settings.NotifyNewDuplicates {
		var newCount int
		err := s.db.GetContext(ctx, &newCount,
			`SELECT COUNT(*) FROM duplicate_groups
			 WHERE merchant_id = $1 AND status = 'pending'
			   AND created_at >= NOW() - INTERVAL '15 minutes'`,
			merchantID,
		)
		if err == nil && newCount > 0 {
			noun := "duplicate group"
			if newCount != 1 {
				noun = "duplicate groups"
			}
			s.create(ctx, merchantID, models.NotificationTypeNewDuplicates,
				fmt.Sprintf("%d new %s detected", newCount, noun),
				"Review them in the Duplicates page before they affect your campaigns.",
				"/duplicates",
			)
		}
	}

	if settings.NotifyHighRisk {
		var riskyCount int
		err := s.db.GetContext(ctx, &riskyCount,
			`SELECT COUNT(*) FROM duplicate_groups
			 WHERE merchant_id = $1 AND status = 'pending' AND risk_level = 'risky'
			   AND created_at >= NOW() - INTERVAL '15 minutes'`,
			merchantID,
		)
		if err == nil && riskyCount > 0 {
			noun := "high-risk group"
			if riskyCount != 1 {
				noun = "high-risk groups"
			}
			s.create(ctx, merchantID, models.NotificationTypeHighRisk,
				fmt.Sprintf("%d %s need your attention", riskyCount, noun),
				"These groups have structural conflicts — review before merging.",
				"/duplicates?status=pending&risk=risky",
			)
		}
	}
}

// OnMergeComplete is called after a successful merge_customers job.
func (s *Service) OnMergeComplete(ctx context.Context, merchantID uuid.UUID, primaryID int64, secondaryCount int) {
	settings, err := s.settingsRepo.Get(ctx, merchantID)
	if err != nil || !settings.NotificationsEnabled || !settings.NotifyBulkComplete {
		return
	}

	body := fmt.Sprintf("Customer %d merged with %d duplicate%s successfully.",
		primaryID, secondaryCount, pluralS(secondaryCount))
	s.create(ctx, merchantID, models.NotificationTypeMergeCompleted,
		"Merge completed",
		body,
		"/history",
	)
}

// OnMergeFailed is called when a merge_customers job fails.
func (s *Service) OnMergeFailed(ctx context.Context, merchantID uuid.UUID, mergeErr error) {
	settings, err := s.settingsRepo.Get(ctx, merchantID)
	if err != nil || !settings.NotificationsEnabled || !settings.NotifyFailures {
		return
	}

	s.create(ctx, merchantID, models.NotificationTypeMergeFailed,
		"Merge failed",
		"A merge job could not complete: "+mergeErr.Error(),
		"/history",
	)
}

// OnDailyScanComplete is called after the scheduled daily scan for a merchant.
func (s *Service) OnDailyScanComplete(ctx context.Context, merchantID uuid.UUID, newGroups int) {
	settings, err := s.settingsRepo.Get(ctx, merchantID)
	if err != nil || !settings.NotificationsEnabled || !settings.NotifyNewDuplicates {
		return
	}
	if newGroups == 0 {
		return
	}
	noun := "duplicate group"
	if newGroups != 1 {
		noun = "duplicate groups"
	}
	s.create(ctx, merchantID, models.NotificationTypeDailyScan,
		fmt.Sprintf("Daily scan: %d %s found", newGroups, noun),
		"Your scheduled daily scan is complete. Review the results.",
		"/duplicates",
	)
}

func (s *Service) create(ctx context.Context, merchantID uuid.UUID, notifType, title, body, actionURL string) {
	n := &models.Notification{
		MerchantID: merchantID,
		Type:       notifType,
		Title:      title,
		Body:       body,
		ActionURL:  actionURL,
	}
	if err := s.notifRepo.Create(ctx, n); err != nil {
		s.log.Warn().Err(err).Str("type", notifType).Msg("notification: create failed")
	}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
