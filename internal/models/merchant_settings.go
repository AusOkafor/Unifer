package models

import "github.com/google/uuid"

type MerchantSettings struct {
	MerchantID           uuid.UUID `db:"merchant_id"`
	AutoDetect           bool      `db:"auto_detect"`
	ConfidenceThreshold  int       `db:"confidence_threshold"`
	RetentionDays        int       `db:"retention_days"`
	NotificationsEnabled bool      `db:"notifications_enabled"`
}
