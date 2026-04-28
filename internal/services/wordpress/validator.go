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
	// Exception: when the local parts are identical or near-identical the domain difference
	// is almost certainly a typo (e.g. gmail.com vs gamil.com). The scorer already surfaces
	// this as EmailLocalExact/EmailLocalFuzzy — don't re-block what the scorer approved.
	if len(domains) > 1 && len(customers) > 1 {
		first := domains[0]
		for i, d := range domains[1:] {
			if d != first {
				localA := emailLocalPart(customers[0].Email)
				localB := emailLocalPart(customers[i+1].Email)
				if localSimilarity(localA, localB) < 0.92 {
					return fmt.Errorf(
						"merge blocked: email domain mismatch (%s vs %s) — verify these are the same person",
						first, d,
					)
				}
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

func emailLocalPart(email string) string {
	parts := strings.SplitN(strings.ToLower(email), "@", 2)
	return parts[0]
}

// localSimilarity returns a 0–1 Levenshtein similarity between two strings.
// Used to detect typo email domains: if the local parts are near-identical the
// domain difference is treated as a typing error, not a different person.
func localSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	// Levenshtein distance
	la, lb := len(a), len(b)
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := dp[i-1][j] + 1
			if dp[i][j-1]+1 < d {
				d = dp[i][j-1] + 1
			}
			if dp[i-1][j-1]+cost < d {
				d = dp[i-1][j-1] + cost
			}
			dp[i][j] = d
		}
	}
	dist := dp[la][lb]
	max := la
	if lb > max {
		max = lb
	}
	return 1.0 - float64(dist)/float64(max)
}
