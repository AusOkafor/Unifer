package dto

import (
	"encoding/json"
	"time"
)


// CustomerDetailDTO is enriched customer data returned in the single-group detail response.
type CustomerDetailDTO struct {
	ShopifyCustomerID int64           `json:"shopify_customer_id"`
	Name              string          `json:"name"`
	Email             string          `json:"email"`
	Phone             string          `json:"phone"`
	Tags              []string        `json:"tags"`
	OrdersCount       int             `json:"orders_count"`
	TotalSpent        string          `json:"total_spent"`
	AddressJSON       json.RawMessage `json:"address_json,omitempty"`
	Note              string          `json:"note,omitempty"`
	State             string          `json:"state,omitempty"`
	VerifiedEmail     bool            `json:"verified_email"`
	ShopifyCreatedAt  *time.Time      `json:"shopify_created_at,omitempty"`
}

// FieldConflictDTO describes a field with differing values across customers.
type FieldConflictDTO struct {
	Field  string   `json:"field"`
	Values []string `json:"values"`
}

// SimulationDTO describes the predicted merge outcome without executing it.
type SimulationDTO struct {
	SurvivingCustomerID int64              `json:"surviving_customer_id"`
	TotalOrderCount     int                `json:"total_order_count"`
	MergedTags          []string           `json:"merged_tags"`
	FieldConflicts      []FieldConflictDTO `json:"field_conflicts"`
}

// ReasonItemDTO is a prioritized human-readable reason for a confidence score.
type ReasonItemDTO struct {
	Text       string `json:"text"`
	Importance string `json:"importance"` // "high" | "medium" | "low"
}

// ConflictItemDTO describes a structural incompatibility between customer records.
type ConflictItemDTO struct {
	Type     string `json:"type"`
	Severity string `json:"severity"` // "high" | "medium" | "low"
	Blocking bool   `json:"blocking"`
}

// FieldBreakdownDTO exposes per-field similarity scores and prioritized
// human-readable reasons for the confidence breakdown UI.
type FieldBreakdownDTO struct {
	EmailScore   float64         `json:"email_score"`
	NameScore    float64         `json:"name_score"`
	PhoneScore   float64         `json:"phone_score"`
	AddressScore float64         `json:"address_score"`
	Reasons      []ReasonItemDTO `json:"reasons,omitempty"`
}

// IntelligenceDTO is the pre-merge analysis embedded in the detail response.
type IntelligenceDTO struct {
	RecommendedPrimary int64             `json:"recommended_primary"`
	ReadinessScore     float64           `json:"readiness_score"`
	Reasoning          []string          `json:"reasoning"`
	RiskFlags          []string          `json:"risk_flags"`
	Simulation         SimulationDTO     `json:"simulation"`
	Breakdown          *FieldBreakdownDTO `json:"breakdown,omitempty"`
	Conflicts          []ConflictItemDTO `json:"conflicts,omitempty"`
	ConflictSeverity   string            `json:"conflict_severity,omitempty"`
	// Summary is a one-line plain-English explanation surfaced at the top of
	// the merge review UI.
	Summary string `json:"summary,omitempty"`
}

// CustomerSummaryDTO is a lightweight customer identity for list-view display.
type CustomerSummaryDTO struct {
	ShopifyCustomerID int64  `json:"shopify_customer_id"`
	Name              string `json:"name"`
	Email             string `json:"email"`
}

// DuplicateGroupResponse is the list-view representation of a duplicate group.
type DuplicateGroupResponse struct {
	ID               string               `json:"id"`
	Confidence       float64              `json:"confidence"`
	RiskLevel        *string              `json:"risk_level,omitempty"` // safe | review | risky
	ReadinessScore   *float64             `json:"readiness_score,omitempty"`
	Status           string               `json:"status"`
	CustomerIDs      []int64              `json:"customer_ids"`
	CustomerSummaries []CustomerSummaryDTO `json:"customer_summaries,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
}

// DuplicateGroupDetailResponse is returned by GET /api/duplicates/:id.
// It extends the list response with enriched customer data and intelligence.
type DuplicateGroupDetailResponse struct {
	DuplicateGroupResponse
	Customers    []CustomerDetailDTO `json:"customers,omitempty"`
	Intelligence *IntelligenceDTO    `json:"intelligence,omitempty"`
}

type PaginatedDuplicates struct {
	Data   []DuplicateGroupResponse `json:"data"`
	Total  int                      `json:"total"`
	Limit  int                      `json:"limit"`
	Offset int                      `json:"offset"`
}

