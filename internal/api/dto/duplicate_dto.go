package dto

import "time"

// CustomerDetailDTO is enriched customer data returned in the single-group detail response.
type CustomerDetailDTO struct {
	ShopifyCustomerID int64    `json:"shopify_customer_id"`
	Name              string   `json:"name"`
	Email             string   `json:"email"`
	Phone             string   `json:"phone"`
	Tags              []string `json:"tags"`
	OrdersCount       int      `json:"orders_count"`
	TotalSpent        string   `json:"total_spent"`
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

// IntelligenceDTO is the pre-merge analysis embedded in the detail response.
type IntelligenceDTO struct {
	RecommendedPrimary int64         `json:"recommended_primary"`
	ReadinessScore     float64       `json:"readiness_score"`
	Reasoning          []string      `json:"reasoning"`
	RiskFlags          []string      `json:"risk_flags"`
	Simulation         SimulationDTO `json:"simulation"`
}

// DuplicateGroupResponse is the list-view representation of a duplicate group.
type DuplicateGroupResponse struct {
	ID             string   `json:"id"`
	Confidence     float64  `json:"confidence"`
	ReadinessScore *float64 `json:"readiness_score,omitempty"`
	Status         string   `json:"status"`
	CustomerIDs    []int64  `json:"customer_ids"`
	CreatedAt      time.Time `json:"created_at"`
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

// CustomerSummary is kept for any legacy internal use.
type CustomerSummary struct {
	ShopifyID int64  `json:"shopify_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
}
