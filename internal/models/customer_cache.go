package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type OrderAddress struct {
	Street  string `json:"street"`
	City    string `json:"city"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
}

type CustomerCache struct {
	ID                uuid.UUID       `db:"id"`
	MerchantID        uuid.UUID       `db:"merchant_id"`
	ShopifyCustomerID int64           `db:"shopify_customer_id"`
	Email             string          `db:"email"`
	Name              string          `db:"name"`
	Phone             string          `db:"phone"`
	AddressJSON       json.RawMessage `db:"address_json"`
	Tags              pq.StringArray  `db:"tags"`
	OrdersCount       int             `db:"orders_count"`
	TotalSpent        string          `db:"total_spent"`
	Note              string          `db:"note"`
	State             string          `db:"state"`
	VerifiedEmail     bool            `db:"verified_email"`
	ShopifyCreatedAt  *time.Time      `db:"shopify_created_at"`
	LastOrderAt       *time.Time      `db:"last_order_at"`
	OrderAddresses    json.RawMessage `db:"order_addresses"`
	OrderNames        pq.StringArray  `db:"order_names"`
	UpdatedAt         time.Time       `db:"updated_at"`
}
