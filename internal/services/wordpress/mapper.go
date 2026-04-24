package wordpress

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// emptyJSONArray is a valid JSON array used to satisfy NOT NULL / JSON-typed
// columns when the WP customer has no per-order data to populate.
var emptyJSONArray = json.RawMessage(`[]`)

// WCCustomer is the shape of a WooCommerce customer as sent by the MergeIQ WP plugin.
// Data is built from WooCommerce order records (wc_get_orders) rather than wp_users,
// so guest customers (no WP account) are included alongside registered ones.
type WCCustomer struct {
	UserID        int64  `json:"user_id"`        // 0 for guest customers (no WP account)
	IsGuest       bool   `json:"is_guest"`        // true when UserID == 0
	Email         string `json:"email"`           // billing email — required, primary identity key
	FirstName     string `json:"first_name"`      // billing first name
	LastName      string `json:"last_name"`       // billing last name
	Phone         string `json:"phone"`           // billing phone
	City          string `json:"city"`            // billing city
	StateProvince string `json:"state"`           // billing state/province
	Postcode      string `json:"postcode"`        // billing postcode
	Country       string `json:"country"`         // billing country code (ISO 3166-1 alpha-2)
	Role          string `json:"role"`            // WP role ("customer", "administrator", …); empty for guests
	RegisteredAt  string `json:"registered_at"`   // ISO 8601 account creation; empty for guests
	OrderCount    int    `json:"order_count"`     // total orders from wc_get_orders()
	TotalSpent    string `json:"total_spent"`     // sum of order totals
}

// MapWCCustomerToCustomerCache converts a WCCustomer into a CustomerCache row.
//
// External IDs: registered users use their real WP user_id; guest customers get a
// stable negative int64 derived from their email via guestExternalID(). Negative IDs
// cannot collide with real WP user IDs (which are always > 0).
//
// Platform="wordpress" isolates these rows from Shopify rows sharing the same merchant.
func MapWCCustomerToCustomerCache(merchantID uuid.UUID, c WCCustomer) *models.CustomerCache {
	name := strings.TrimSpace(c.FirstName + " " + c.LastName)

	var createdAt *time.Time
	if c.RegisteredAt != "" {
		if t, err := time.Parse(time.RFC3339, c.RegisteredAt); err == nil {
			createdAt = &t
		}
	}

	externalID := c.UserID
	if c.IsGuest || c.UserID == 0 {
		externalID = guestExternalID(c.Email)
	}

	addr := buildWCAddress(c)

	return &models.CustomerCache{
		MerchantID:        merchantID,
		Platform:          "wordpress",
		ShopifyCustomerID: externalID,
		Email:             utils.NormalizeEmail(c.Email),
		Name:              name,
		Phone:             c.Phone,
		Tags:              pq.StringArray{},
		AddressJSON:       models.NullableJSON(addr),
		OrdersCount:       c.OrderCount,
		TotalSpent:        c.TotalSpent,
		State:             c.Role, // "administrator" is blocked by WPValidator
		ShopifyCreatedAt:  createdAt,
		OrderAddresses:    &emptyJSONArray, // no per-order address history in WC sync v1
	}
}

// guestExternalID returns a stable negative int64 for a guest customer who has no WP
// user account. Real WP user IDs are always > 0, so negative values unambiguously
// identify guest-only order streams and cannot collide with registered-user IDs.
func guestExternalID(email string) int64 {
	h := fnv.New64a()
	h.Write([]byte(strings.ToLower(strings.TrimSpace(email))))
	// Right-shift clears bit 63 to keep the uint64 in int64 range,
	// then negate so the result is always negative and non-zero.
	return -(int64(h.Sum64()>>1) + 1)
}

func buildWCAddress(c WCCustomer) json.RawMessage {
	if c.City == "" && c.Postcode == "" && c.Country == "" {
		return nil
	}
	b, _ := json.Marshal(models.OrderAddress{
		City:    c.City,
		Zip:     c.Postcode,
		Country: c.Country,
	})
	return b
}

// WCCustomerGID returns the pseudo-GID stored in MergeRecord.ResultingCustomerGID
// for WooCommerce merges. Registered users get a user-ID GID; guest survivors
// (rare: only if a guest was the primary and no account was created) get an email GID.
func WCCustomerGID(userID int64, email string) string {
	if userID > 0 {
		return fmt.Sprintf("wc://User/%d", userID)
	}
	return fmt.Sprintf("wc://Guest/%s", email)
}
