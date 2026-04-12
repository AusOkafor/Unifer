package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	JobTypeSyncCustomers    = "sync_customers"
	JobTypeDetectDuplicates = "detect_duplicates"
	JobTypeMergeCustomers   = "merge_customers"
	JobTypeRestoreSnapshot  = "restore_snapshot"

	JobStatusQueued     = "queued"
	JobStatusProcessing = "processing"
	JobStatusCompleted  = "completed"
	JobStatusFailed     = "failed"
)

type Job struct {
	ID         uuid.UUID       `db:"id"`
	MerchantID uuid.UUID       `db:"merchant_id"`
	Type       string          `db:"type"`
	Status     string          `db:"status"`
	Payload    json.RawMessage  `db:"payload"`
	Result     *json.RawMessage `db:"result"`
	Retries    int             `db:"retries"`
	CreatedAt  time.Time       `db:"created_at"`
	UpdatedAt  time.Time       `db:"updated_at"`
}
