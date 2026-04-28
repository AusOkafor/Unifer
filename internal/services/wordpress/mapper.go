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

// WCOrder holds the order-level data used for behavioral identity signals.
// The plugin should include the last 20 completed orders per customer.
// Shipping fields are preferred over billing for address comparison because
// billing addresses are more often shared (e.g. family accounts) while
// shipping addresses tend to be per-person.
type WCOrder struct {
	ShippingFirstName string `json:"shipping_first_name"` // $order->get_shipping_first_name()
	ShippingLastName  string `json:"shipping_last_name"`  // $order->get_shipping_last_name()
	ShippingAddress1  string `json:"shipping_address1"`   // $order->get_shipping_address_1()
	ShippingCity      string `json:"shipping_city"`       // $order->get_shipping_city()
	ShippingPostcode  string `json:"shipping_postcode"`   // $order->get_shipping_postcode()
	ShippingCountry   string `json:"shipping_country"`    // $order->get_shipping_country()
	DateCreated       string `json:"date_created"`        // $order->get_date_created()->format('c') — ISO 8601
}

// WCCustomer is the shape of a WooCommerce customer as sent by the MergeIQ WP plugin.
// Data is built from WooCommerce order records (wc_get_orders) rather than wp_users,
// so guest customers (no WP account) are included alongside registered ones.
type WCCustomer struct {
	UserID        int64     `json:"user_id"`        // 0 for guest customers (no WP account)
	IsGuest       bool      `json:"is_guest"`       // true when UserID == 0
	Email         string    `json:"email"`          // billing email — required, primary identity key
	FirstName     string    `json:"first_name"`     // billing first name
	LastName      string    `json:"last_name"`      // billing last name
	Phone         string    `json:"phone"`          // billing phone
	Address1      string    `json:"address1"`       // billing_address_1 from WP user meta
	Address2      string    `json:"address2"`       // billing_address_2 from WP user meta
	City          string    `json:"city"`           // billing city
	StateProvince string    `json:"state"`          // billing state/province
	Postcode      string    `json:"postcode"`       // billing postcode
	Country       string    `json:"country"`        // billing country code (ISO 3166-1 alpha-2)
	Role          string    `json:"role"`           // WP role ("customer", "administrator", …); empty for guests
	RegisteredAt  string    `json:"registered_at"`  // ISO 8601 account creation; empty for guests
	OrderCount    int       `json:"order_count"`    // total orders from wc_get_orders()
	TotalSpent    string    `json:"total_spent"`    // sum of order totals
	CustomerNote  string    `json:"customer_note"`  // most recent non-empty checkout note; "" when none
	Orders        []WCOrder `json:"orders,omitempty"` // last ≤20 completed orders — drives behavioral signals
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
	orderAddresses, orderNames, lastOrderAt := extractWCOrderSignals(c.Orders)

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
		Note:              c.CustomerNote,
		State:             c.Role, // "administrator" is blocked by WPValidator
		ShopifyCreatedAt:  createdAt,
		LastOrderAt:       lastOrderAt,
		OrderAddresses:    orderAddresses,
		OrderNames:        pq.StringArray(orderNames),
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
	if c.Address1 == "" && c.City == "" && c.StateProvince == "" && c.Postcode == "" && c.Country == "" {
		return nil
	}
	b, _ := json.Marshal(models.OrderAddress{
		Address1: c.Address1,
		Address2: c.Address2,
		City:     c.City,
		State:    c.StateProvince,
		Zip:      c.Postcode,
		Country:  c.Country,
	})
	return b
}

// extractWCOrderSignals converts a slice of WCOrder records into the three
// CustomerCache fields consumed by the behavioral scorer:
//   - OrderAddresses — unique shipping addresses (city+postcode+country present)
//   - OrderNames     — deduplicated checkout names (shipping first+last)
//   - LastOrderAt    — timestamp of the most recent order
//
// All three are nil/empty when c.Orders is empty, which gracefully degrades to
// profile-only scoring — identical to pre-OI behaviour.
func extractWCOrderSignals(orders []WCOrder) (models.NullableJSON, []string, *time.Time) {
	if len(orders) == 0 {
		return nil, nil, nil
	}

	var addrs []models.OrderAddress
	var names []string
	seenNames := make(map[string]struct{})
	var lastOrderAt *time.Time

	for _, o := range orders {
		// Address — only include when there is enough data for a meaningful comparison.
		if o.ShippingCity != "" || o.ShippingPostcode != "" || o.ShippingCountry != "" {
			addrs = append(addrs, models.OrderAddress{
				Address1: o.ShippingAddress1,
				City:     o.ShippingCity,
				Zip:      o.ShippingPostcode,
				Country:  o.ShippingCountry,
			})
		}

		// Name — deduplicate so the scorer doesn't see the same name 20 times.
		n := strings.TrimSpace(o.ShippingFirstName + " " + o.ShippingLastName)
		if n != "" {
			if _, dup := seenNames[n]; !dup {
				seenNames[n] = struct{}{}
				names = append(names, n)
			}
		}

		// LastOrderAt — keep the most recent timestamp.
		if o.DateCreated != "" {
			t, err := time.Parse(time.RFC3339, o.DateCreated)
			if err == nil && (lastOrderAt == nil || t.After(*lastOrderAt)) {
				tCopy := t
				lastOrderAt = &tCopy
			}
		}
	}

	var addrJSON models.NullableJSON
	if len(addrs) > 0 {
		b, _ := json.Marshal(addrs)
		addrJSON = models.NullableJSON(b)
	}

	return addrJSON, names, lastOrderAt
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
