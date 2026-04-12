package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

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
	UpdatedAt         time.Time       `db:"updated_at"`
}
