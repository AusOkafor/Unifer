package identity

import (
	"strconv"
	"strings"
	"time"

	"merger/backend/internal/models"
)

const anchorThreshold = 0.50

// anchorScore rates the reliability of a single customer record as an identity
// anchor on a 0–1 scale. Higher means the record is more trustworthy.
//
// Score components (maximum 1.0):
//   - Verified email  (+0.30): account was explicitly claimed by the owner
//   - Order history   (+0.25): real transacting customers aren't ghosts
//   - Account age     (+0.25): established accounts are less likely throwaway
//   - Total spend     (+0.20): high-spend accounts are carefully maintained
//
// A score ≥ anchorThreshold (0.50) qualifies the customer as a strong anchor.
func anchorScore(c models.CustomerCache) float64 {
	score := 0.0

	// Verified email: Shopify confirmed the address was claimed.
	if c.VerifiedEmail {
		score += 0.30
	}

	// Order history: ghost accounts almost never transact.
	switch {
	case c.OrdersCount >= 10:
		score += 0.25
	case c.OrdersCount >= 3:
		score += 0.15
	case c.OrdersCount >= 1:
		score += 0.08
	}

	// Account age: throwaway accounts are typically new.
	if c.ShopifyCreatedAt != nil {
		months := time.Since(*c.ShopifyCreatedAt).Hours() / 730 // 730 h ≈ 1 month
		switch {
		case months >= 24:
			score += 0.25
		case months >= 6:
			score += 0.15
		case months >= 1:
			score += 0.05
		}
	}

	// Total spend: merchants carefully maintain high-value accounts.
	if spent := parseSpent(c.TotalSpent); spent > 0 {
		switch {
		case spent >= 500:
			score += 0.20
		case spent >= 100:
			score += 0.12
		case spent >= 10:
			score += 0.05
		}
	}

	if score > 1.0 {
		return 1.0
	}
	return score
}

// clusterHasAnchor returns true if at least one cluster member has an anchor
// score ≥ anchorThreshold. A cluster with no strong anchor consists entirely
// of ghost-like or newly-created records — dangerous to merge automatically
// because the cost of a wrong merge is high and the signal quality is low.
func clusterHasAnchor(members []models.CustomerCache) bool {
	for _, c := range members {
		if anchorScore(c) >= anchorThreshold {
			return true
		}
	}
	return false
}

// parseSpent converts a Shopify total_spent string ("123.45") to float64.
// Returns 0 on empty input or any parse error.
func parseSpent(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
