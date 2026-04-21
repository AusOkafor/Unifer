package models

import (
	"time"

	"github.com/google/uuid"
)

type WPRefreshToken struct {
	ID         uuid.UUID `db:"id"`
	MerchantID uuid.UUID `db:"merchant_id"`
	TokenHash  string    `db:"token_hash"` // SHA-256 hex — raw token is never stored
	IssuedAt   time.Time `db:"issued_at"`
	ExpiresAt  time.Time `db:"expires_at"`
	Revoked    bool      `db:"revoked"`
}
