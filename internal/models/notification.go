package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	NotificationTypeNewDuplicates  = "new_duplicates"
	NotificationTypeHighRisk       = "high_risk"
	NotificationTypeMergeCompleted = "merge_completed"
	NotificationTypeMergeFailed    = "merge_failed"
	NotificationTypeDailyScan      = "daily_scan"
)

type Notification struct {
	ID         uuid.UUID `db:"id"          json:"id"`
	MerchantID uuid.UUID `db:"merchant_id" json:"-"`
	Type       string    `db:"type"        json:"type"`
	Title      string    `db:"title"       json:"title"`
	Body       string    `db:"body"        json:"body"`
	IsRead     bool      `db:"is_read"     json:"is_read"`
	ActionURL  string    `db:"action_url"  json:"action_url"`
	CreatedAt  time.Time `db:"created_at"  json:"created_at"`
}
