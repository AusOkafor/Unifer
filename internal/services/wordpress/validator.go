package wordpress

import (
	"context"
	"fmt"

	"merger/backend/internal/models"
)

// WPValidator blocks merges involving administrator accounts or customers
// with missing email addresses.
type WPValidator struct{}

func NewWPValidator() *WPValidator { return &WPValidator{} }

func (v *WPValidator) Validate(_ context.Context, customers []models.CustomerCache) error {
	for _, c := range customers {
		if c.State == "administrator" {
			return fmt.Errorf("merge blocked: customer %d is an administrator account", c.ShopifyCustomerID)
		}
		if c.Email == "" {
			return fmt.Errorf("merge blocked: customer %d has no email address", c.ShopifyCustomerID)
		}
	}
	return nil
}
