package merge

import (
	"context"
	"fmt"
	"strings"

	shopifysvc "merger/backend/internal/services/shopify"
)

// Validator checks Shopify constraints before a merge is allowed to proceed.
type Validator struct{}

func NewValidator() *Validator {
	return &Validator{}
}

// Validate checks all Shopify constraints that would block a safe merge.
// If any constraint is violated, it returns a descriptive error.
func (v *Validator) Validate(ctx context.Context, customers []shopifysvc.ShopifyCustomer) error {
	for _, c := range customers {
		if err := v.validateCustomer(c); err != nil {
			return err
		}
	}
	return nil
}

func (v *Validator) validateCustomer(c shopifysvc.ShopifyCustomer) error {
	// Check for subscription tags (basic heuristic — full check requires metafields API)
	for _, tag := range strings.Split(c.Tags, ",") {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag == "subscriber" || tag == "active_subscription" || tag == "subscription" {
			return fmt.Errorf("customer %d has active subscription tag %q — cannot merge", c.ID, tag)
		}
	}

	// Check state — disabled/blocked customers may have special handling
	if c.State == "declined" {
		return fmt.Errorf("customer %d is in declined state — review before merging", c.ID)
	}

	return nil
}
