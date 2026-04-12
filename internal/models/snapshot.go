package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Snapshot struct {
	ID            uuid.UUID       `db:"id"`
	MerchantID    uuid.UUID       `db:"merchant_id"`
	MergeRecordID *uuid.UUID      `db:"merge_record_id"`
	Data          json.RawMessage `db:"data"`
	CreatedAt     time.Time       `db:"created_at"`
}
