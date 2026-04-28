package wordpress

import (
	"context"
	"fmt"
	"time"

	"merger/backend/internal/models"
)

const recentActivityWindow = 2 * time.Hour

// privilegedRoles are WP roles that carry elevated permissions.
// Merging users with mismatched privileged roles is blocked.
var privilegedRoles = map[string]bool{
	"administrator": true,
	"editor":        true,
	"author":        true,
	"contributor":   true,
}

// WPValidator blocks merges that are unsafe for WordPress:
//   - administrator accounts (irrecoverable privilege escalation)
//   - missing email on any customer
//   - conflicting roles (e.g. editor + subscriber)
//   - any customer with order activity in the last 2 hours (race condition guard)
//
// Email domain differences are intentionally not blocked: the scorer already
// penalises them, and the validator never sees the user's Merge Composer
// selections (the discarded email's domain is irrelevant post-merge).
type WPValidator struct{}

func NewWPValidator() *WPValidator { return &WPValidator{} }

func (v *WPValidator) Validate(_ context.Context, customers []models.CustomerCache) error {
	if len(customers) == 0 {
		return nil
	}

	var roles []string
	now := time.Now()

	for _, c := range customers {
		// Hard block: administrator accounts.
		if c.State == "administrator" {
			return fmt.Errorf("merge blocked: customer %d is an administrator account", c.ShopifyCustomerID)
		}

		// Hard block: missing email.
		if c.Email == "" {
			return fmt.Errorf("merge blocked: customer %d has no email address", c.ShopifyCustomerID)
		}

		// Collect role for conflict check.
		if c.State != "" {
			roles = append(roles, c.State)
		}

		// Block if recent order activity (race condition: merge during active session).
		if c.LastOrderAt != nil && now.Sub(*c.LastOrderAt) < recentActivityWindow {
			return fmt.Errorf(
				"merge blocked: customer %d has order activity within the last 2 hours — retry later",
				c.ShopifyCustomerID,
			)
		}
	}

	// NOTE: email domain mismatch is intentionally NOT blocked here.
	// The scorer already applies a DifferentEmailDomain penalty — if a pair reached
	// clustering it means the scorer weighed the domain difference and still found
	// sufficient evidence. More importantly, the validator never sees the user's
	// Merge Composer selections: the user may have explicitly chosen to keep the
	// primary email and discard the secondary's, making the secondary's domain
	// irrelevant to the outcome. Re-blocking here overrides an informed user decision.

	// Block on role conflict when any privileged role is involved.
	// Two subscribers merging is fine; an editor merging with a subscriber is not.
	if len(roles) > 1 {
		first := roles[0]
		for _, r := range roles[1:] {
			if r != first && (privilegedRoles[first] || privilegedRoles[r]) {
				return fmt.Errorf(
					"merge blocked: role conflict (%s vs %s) — privileged roles must not be merged with different roles",
					first, r,
				)
			}
		}
	}

	return nil
}


