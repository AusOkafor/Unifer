package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type MergeRecord struct {
	ID                   uuid.UUID     `db:"id"`
	MerchantID           uuid.UUID     `db:"merchant_id"`
	PrimaryCustomerID    int64         `db:"primary_customer_id"`
	SecondaryCustomerIDs pq.Int64Array `db:"secondary_customer_ids"`
	OrdersMoved          int           `db:"orders_moved"`
	PerformedBy          string        `db:"performed_by"`
	SnapshotID           *uuid.UUID    `db:"snapshot_id"`
	CreatedAt            time.Time     `db:"created_at"`
}
