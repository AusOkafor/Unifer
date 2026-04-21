package models

import (
	"time"

	"github.com/google/uuid"
)

type Merchant struct {
	ID             uuid.UUID `db:"id"`
	ShopDomain     string    `db:"shop_domain"`
	AccessTokenEnc string    `db:"access_token_enc"`
	Platform       string    `db:"platform"` // "shopify" | "wordpress"
	CreatedAt      time.Time `db:"created_at"`
}
