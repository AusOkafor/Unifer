package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"merger/backend/internal/models"
)

type NotificationRepository interface {
	Create(ctx context.Context, n *models.Notification) error
	ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Notification, error)
	UnreadCount(ctx context.Context, merchantID uuid.UUID) (int, error)
	MarkRead(ctx context.Context, id uuid.UUID, merchantID uuid.UUID) error
	MarkAllRead(ctx context.Context, merchantID uuid.UUID) error
	// DeleteOld removes notifications older than 30 days to keep the table lean.
	DeleteOld(ctx context.Context) error
}

type notificationRepo struct {
	db *sqlx.DB
}

func NewNotificationRepo(db *sqlx.DB) NotificationRepository {
	return &notificationRepo{db: db}
}

func (r *notificationRepo) Create(ctx context.Context, n *models.Notification) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notifications (merchant_id, type, title, body, action_url)
		 VALUES ($1, $2, $3, $4, $5)`,
		n.MerchantID, n.Type, n.Title, n.Body, n.ActionURL,
	)
	if err != nil {
		return fmt.Errorf("notification create: %w", err)
	}
	return nil
}

func (r *notificationRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Notification, error) {
	var ns []models.Notification
	err := r.db.SelectContext(ctx, &ns,
		`SELECT id, merchant_id, type, title, body, is_read, action_url, created_at
		 FROM notifications
		 WHERE merchant_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		merchantID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("notification list: %w", err)
	}
	return ns, nil
}

func (r *notificationRepo) UnreadCount(ctx context.Context, merchantID uuid.UUID) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM notifications WHERE merchant_id = $1 AND is_read = false`,
		merchantID,
	)
	if err != nil {
		return 0, fmt.Errorf("notification unread count: %w", err)
	}
	return count, nil
}

func (r *notificationRepo) MarkRead(ctx context.Context, id uuid.UUID, merchantID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE notifications SET is_read = true WHERE id = $1 AND merchant_id = $2`,
		id, merchantID,
	)
	if err != nil {
		return fmt.Errorf("notification mark read: %w", err)
	}
	return nil
}

func (r *notificationRepo) MarkAllRead(ctx context.Context, merchantID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE notifications SET is_read = true WHERE merchant_id = $1 AND is_read = false`,
		merchantID,
	)
	if err != nil {
		return fmt.Errorf("notification mark all read: %w", err)
	}
	return nil
}

func (r *notificationRepo) DeleteOld(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE created_at < NOW() - INTERVAL '30 days'`,
	)
	if err != nil {
		return fmt.Errorf("notification delete old: %w", err)
	}
	return nil
}
