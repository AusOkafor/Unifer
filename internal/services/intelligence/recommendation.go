package intelligence

import (
	"strconv"
	"strings"

	"merger/backend/internal/models"
)

// recommendPrimary selects the best customer to survive the merge.
//
// Priority (descending):
//  1. Verified email
//  2. Highest order count
//  3. Highest total_spent (lifetime value)
//  4. Older account (earlier shopify_created_at = more established)
//  5. Most recently updated (proxy for activity)
func recommendPrimary(customers []models.CustomerCache) int64 {
	if len(customers) == 0 {
		return 0
	}
	best := customers[0]
	for _, c := range customers[1:] {
		if betterPrimary(c, best) {
			best = c
		}
	}
	return best.ShopifyCustomerID
}

func betterPrimary(candidate, current models.CustomerCache) bool {
	// 1. Verified email signals a more authoritative account
	if candidate.VerifiedEmail != current.VerifiedEmail {
		return candidate.VerifiedEmail
	}
	// 2. More orders wins
	if candidate.OrdersCount != current.OrdersCount {
		return candidate.OrdersCount > current.OrdersCount
	}
	// 3. Higher lifetime value
	candidateSpent := parseMoney(candidate.TotalSpent)
	currentSpent := parseMoney(current.TotalSpent)
	if candidateSpent != currentSpent {
		return candidateSpent > currentSpent
	}
	// 4. Older account is the "original" — prefer the earlier creation date
	if candidate.ShopifyCreatedAt != nil && current.ShopifyCreatedAt != nil {
		if !candidate.ShopifyCreatedAt.Equal(*current.ShopifyCreatedAt) {
			return candidate.ShopifyCreatedAt.Before(*current.ShopifyCreatedAt)
		}
	} else if candidate.ShopifyCreatedAt != nil {
		return true
	}
	// 5. Recency as final tiebreaker
	return candidate.UpdatedAt.After(current.UpdatedAt)
}

// parseMoney converts a Shopify total_spent string (e.g. "125.50") to cents
// as an integer, so float comparisons are exact.
func parseMoney(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f * 100)
}
