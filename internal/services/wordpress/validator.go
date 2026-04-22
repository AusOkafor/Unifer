package wordpress

import (
	"context"
	"fmt"
	"strings"
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
//   - mismatched email domains (likely different people)
//   - conflicting roles (e.g. editor + subscriber)
//   - any customer with order activity in the last 2 hours (race condition guard)
type WPValidator struct{}

func NewWPValidator() *WPValidator { return &WPValidator{} }

func (v *WPValidator) Validate(_ context.Context, customers []models.CustomerCache) error {
	if len(customers) == 0 {
		return nil
	}

	var domains []string
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

		// Collect email domain for mismatch check.
		domain := emailDomain(c.Email)
		if domain != "" {
			domains = append(domains, domain)
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

	// Block on email domain mismatch — different domains almost always mean different people.
	if len(domains) > 1 {
		first := domains[0]
		for _, d := range domains[1:] {
			if d != first {
				return fmt.Errorf(
					"merge blocked: email domain mismatch (%s vs %s) — verify these are the same person",
					first, d,
				)
			}
		}
	}

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

func emailDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		return strings.ToLower(parts[1])
	}
	return ""
}
