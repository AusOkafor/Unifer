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
	Status           string          `db:"status"`   // pending | reviewed | merged | dismissed
	RiskLevel        *string         `db:"risk_level"` // safe | review | risky
	ReadinessScore   *float64        `db:"readiness_score"`
	IntelligenceJSON json.RawMessage `db:"intelligence_json"`
	CreatedAt        time.Time       `db:"created_at"`
	// MergedAt is set when status transitions to "merged".
	// Learning signal: at this confidence + breakdown, a human confirmed these
	// accounts were the same person. Used for future scoring calibration.
	MergedAt *time.Time `db:"merged_at"`
	// DismissedAt is set when the group is dismissed as not a duplicate.
	// Together with DismissReason it forms the negative feedback loop.
	DismissedAt   *time.Time `db:"dismissed_at"`
	DismissReason *string    `db:"dismiss_reason"`
	// ConfirmedByUser is true when a human manually triggered the merge
	// (as opposed to an automated bulk merge). A stronger learning signal.
	ConfirmedByUser bool `db:"confirmed_by_user"`

	// BusinessRiskLevel is the commercial risk of merging this cluster,
	// independent of identity confidence: "high" | "medium" | "low" | nil.
	// Set at detection time based on spend disparity, order count delta, age gap.
	BusinessRiskLevel *string `db:"business_risk_level"`

	// ImpactScore = cluster_size × avg_customer_value. Used as a blast-radius
	// guardrail: high-value clusters require manual confirmation even when
	// identity confidence is strong.
	ImpactScore *float64 `db:"impact_score"`
}
