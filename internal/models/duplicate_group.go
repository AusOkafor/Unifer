package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type DuplicateGroup struct {
	ID               uuid.UUID       `db:"id"`
	MerchantID       uuid.UUID       `db:"merchant_id"`
	GroupHash        string          `db:"group_hash"`
	CustomerIDs      pq.Int64Array   `db:"customer_ids"`
	ConfidenceScore  float64         `db:"confidence_score"`
	Status           string          `db:"status"`   // pending | reviewed | merged
	RiskLevel        *string         `db:"risk_level"` // safe | review | risky
	ReadinessScore   *float64        `db:"readiness_score"`
	IntelligenceJSON json.RawMessage `db:"intelligence_json"`
	CreatedAt        time.Time       `db:"created_at"`
	// MergedAt is set when status transitions to "merged".
	// It is a learning signal: at this confidence + breakdown, a human confirmed
	// these accounts were the same person. Used for future scoring calibration.
	MergedAt         *time.Time      `db:"merged_at"`
}
