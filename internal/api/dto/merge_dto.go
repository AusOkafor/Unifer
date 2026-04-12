package dto

import "time"

type MergeExecuteRequest struct {
	GroupID           string  `json:"group_id" binding:"required"`
	PrimaryCustomerID int64   `json:"primary_customer_id" binding:"required"`
	SecondaryIDs      []int64 `json:"secondary_ids" binding:"required,min=1"`
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
