package models

import (
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// NullableJSON is a json.RawMessage that correctly scans SQL NULL as nil.
// database/sql cannot scan NULL into json.RawMessage directly because the
// named type lacks a sql.Scanner implementation.
type NullableJSON []byte

func (n *NullableJSON) Scan(v interface{}) error {
	if v == nil {
		*n = nil
		return nil
	}
	switch b := v.(type) {
	case []byte:
		*n = append((*n)[:0], b...)
	case string:
		*n = []byte(b)
	default:
		return fmt.Errorf("NullableJSON: unsupported type %T", v)
	}
	return nil
}

// Value implements driver.Valuer so pgx (and pq) send NullableJSON as a text
// string rather than a bytea hex literal. pgx in simple-query protocol encodes
// bare []byte as \x<hex>, which PostgreSQL rejects for jsonb columns. Returning
// string(n) lets pgx emit a quoted text literal that PostgreSQL parses as JSON.
func (n NullableJSON) Value() (driver.Value, error) {
	if len(n) == 0 {
		return nil, nil
	}
	return string(n), nil
}

func (n NullableJSON) MarshalJSON() ([]byte, error) {
	if len(n) == 0 {
		return []byte("null"), nil
	}
	return []byte(n), nil
}

type OrderAddress struct {
	Street  string `json:"street"`
	City    string `json:"city"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
}

type CustomerCache struct {
	ID                uuid.UUID    `db:"id"`
	MerchantID        uuid.UUID    `db:"merchant_id"`
	Platform          string       `db:"platform"` // "shopify" | "wordpress"
	ShopifyCustomerID int64        `db:"shopify_customer_id"`
	Email             string       `db:"email"`
	Name              string       `db:"name"`
	Phone             string       `db:"phone"`
	AddressJSON       NullableJSON `db:"address_json"`
	Tags              pq.StringArray  `db:"tags"`
	OrdersCount       int             `db:"orders_count"`
	TotalSpent        string          `db:"total_spent"`
	Note              string          `db:"note"`
	State             string          `db:"state"`
	VerifiedEmail     bool            `db:"verified_email"`
	ShopifyCreatedAt  *time.Time      `db:"shopify_created_at"`
	LastOrderAt       *time.Time      `db:"last_order_at"`
	OrderAddresses    NullableJSON     `db:"order_addresses"`
	OrderNames        pq.StringArray  `db:"order_names"`
	UpdatedAt         time.Time       `db:"updated_at"`
}
