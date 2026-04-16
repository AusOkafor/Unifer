package dto

import (
	"time"

	"merger/backend/internal/services/intelligence"
)


type MergeExecuteRequest struct {
	GroupID           string  `json:"group_id" binding:"required"`
	PrimaryCustomerID int64   `json:"primary_customer_id" binding:"required"`
	SecondaryIDs      []int64 `json:"secondary_ids" binding:"required,min=1"`
	// OverrideDisabled bypasses the disabled_account hard block when the user
	// has explicitly acknowledged that the merge may reactivate the account.
	// All other hard blocks (fraud tags, different country) remain enforced.
	OverrideDisabled  bool    `json:"override_disabled"`
}

type MergeExecuteResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

type MergeRecordResponse struct {
	ID                   string    `json:"id"`
	PrimaryCustomerID    int64     `json:"primary_customer_id"`
	SecondaryCustomerIDs []int64   `json:"secondary_customer_ids"`
	OrdersMoved          int       `json:"orders_moved"`
	PerformedBy          string    `json:"performed_by"`
	SnapshotID           *string   `json:"snapshot_id,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type PaginatedMergeRecords struct {
	Data   []MergeRecordResponse `json:"data"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

// SafeBulkMergeResponse is returned when safe-bulk merge jobs are dispatched.
type SafeBulkMergeResponse struct {
	Queued  int      `json:"queued"`
	JobIDs  []string `json:"job_ids"`
	Skipped int      `json:"skipped"`
}

// MergeValidateRequest is sent by the frontend after the user makes Merge
// Composer selections. The backend re-runs conflict detection and returns the
// split blocking/resolvable result so the UI can gate the merge button correctly.
type MergeValidateRequest struct {
	PrimaryCustomerID int64    `json:"primary_id" binding:"required"`
	SecondaryIDs      []int64  `json:"secondary_ids" binding:"required,min=1"`
	// Selection captures the user's field picks — "primary" | "secondary" | "".
	Selection struct {
		Email   string `json:"email"`
		Phone   string `json:"phone"`
		Address string `json:"address"`
		Name    string `json:"name"`
	} `json:"selection"`
	// OverrideDisabled treats the disabled_account conflict as non-blocking
	// when the user has acknowledged the risk.
	OverrideDisabled bool `json:"override_disabled"`
}

// MergeValidateResponse is returned by POST /api/merge/validate.
type MergeValidateResponse struct {
	HasBlockingConflicts bool                        `json:"has_blocking_conflicts"`
	BlockingConflicts    []intelligence.ConflictItem `json:"blocking_conflicts"`
	ResolvableConflicts  []intelligence.ConflictItem `json:"resolvable_conflicts"`
	IsReadyToMerge      bool                        `json:"is_ready_to_merge"`
}

// BulkPreviewResponse summarises what a safe-bulk merge would do.
type BulkPreviewResponse struct {
	SafeGroupCount   int     `json:"safe_group_count"`
	TotalCustomers   int     `json:"total_customers"`
	CombinedOrders   int     `json:"combined_orders"`
	CombinedRevenue  float64 `json:"combined_revenue"`
	ConflictCount    int     `json:"conflict_count"`
}
