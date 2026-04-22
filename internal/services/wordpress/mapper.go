package wordpress

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// WPUser is the shape of a WordPress user as sent by the MergeIQ WP plugin.
type WPUser struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Phone        string `json:"phone"`
	City         string `json:"city"`
	Zip          string `json:"zip"`
	Country      string `json:"country"`
	Role         string `json:"role"`           // "administrator", "customer", etc.
	RegisteredAt string `json:"registered_at"`  // ISO 8601
	OrderCount   int    `json:"order_count"`
	TotalSpent   string `json:"total_spent"`
}

// MapWPUserToCustomerCache converts a WPUser into a CustomerCache row.
// WP user IDs are stored in ShopifyCustomerID (int64 column); Platform="wordpress"
// keeps them isolated from Shopify rows that share the same merchant.
func MapWPUserToCustomerCache(merchantID uuid.UUID, u WPUser) *models.CustomerCache {
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		name = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}

	var createdAt *time.Time
	if u.RegisteredAt != "" {
		if t, err := time.Parse(time.RFC3339, u.RegisteredAt); err == nil {
			createdAt = &t
		}
	}

	addr := buildAddress(u)

	return &models.CustomerCache{
		MerchantID:        merchantID,
		Platform:          "wordpress",
		ShopifyCustomerID: u.ID, // WP user ID stored here; Platform disambiguates
		Email:             utils.NormalizeEmail(u.Email),
		Name:              name,
		Phone:             u.Phone,
		Tags:              pq.StringArray{},
		AddressJSON:       models.NullableJSON(addr),
		OrdersCount:       u.OrderCount,
		TotalSpent:        u.TotalSpent,
		State:             u.Role, // "administrator" is blocked by WPValidator
		ShopifyCreatedAt:  createdAt,
	}
}

func buildAddress(u WPUser) json.RawMessage {
	if u.City == "" && u.Zip == "" && u.Country == "" {
		return nil
	}
	b, _ := json.Marshal(models.OrderAddress{
		City:    u.City,
		Zip:     u.Zip,
		Country: u.Country,
	})
	return b
}

// WPUserGID returns the pseudo-GID stored in MergeRecord.ResultingCustomerGID
// for WordPress merges so the field is non-empty and traceable.
func WPUserGID(userID int64) string {
	return fmt.Sprintf("wp://User/%d", userID)
}
