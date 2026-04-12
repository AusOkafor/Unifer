package merge

import (
	"context"
	"fmt"
	"strings"

	"merger/backend/internal/models"
)

// Validator checks constraints before a merge is allowed to proceed.
type Validator struct{}

func NewValidator() *Validator {
	return &Validator{}
}

// Validate checks all constraints that would block a safe merge.
func (v *Validator) Validate(_ context.Context, customers []models.CustomerCache) error {
	for _, c := range customers {
		if err := v.validateCustomer(c); err != nil {
			return err
		}
	}
	return nil
}

func (v *Validator) validateCustomer(c models.CustomerCache) error {
	for _, tag := range c.Tags {
		t := strings.ToLower(strings.TrimSpace(tag))
		if t == "subscriber" || t == "active_subscription" || t == "subscription" {
			return fmt.Errorf("customer %d has active subscription tag %q — cannot merge", c.ShopifyCustomerID, tag)
		}
	}
	return nil
}
