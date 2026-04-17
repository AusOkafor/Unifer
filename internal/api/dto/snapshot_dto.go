package dto

import "time"

// SnapshotCustomerPreview is a safe, UI-oriented view of a customer row in snapshot JSON.
type SnapshotCustomerPreview struct {
	ShopifyCustomerID int64    `json:"shopify_customer_id"`
	Email             string   `json:"email"`
	FirstName         string   `json:"first_name"`
	LastName          string   `json:"last_name"`
	DisplayName       string   `json:"display_name"`
	Phone             string   `json:"phone"`
	Tags              []string `json:"tags"`
	OrdersCount       int      `json:"orders_count"`
	TotalSpent        string   `json:"total_spent"`
	CreatedAt         string   `json:"created_at,omitempty"`
	AddressSummary    string   `json:"address_summary,omitempty"`
}

// SnapshotPreviewResponse is returned by GET /api/snapshot/:id.
type SnapshotPreviewResponse struct {
	SnapshotID    string                    `json:"snapshot_id"`
	CreatedAt     time.Time                 `json:"created_at"`
	MergeRecordID *string                   `json:"merge_record_id,omitempty"`
	Customers     []SnapshotCustomerPreview `json:"customers"`
}
