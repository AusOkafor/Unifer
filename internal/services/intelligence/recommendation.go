package intelligence

import "merger/backend/internal/models"

// recommendPrimary selects the best customer to survive the merge.
//
// Priority (descending):
//  1. Highest order count
//  2. Has email (over one that doesn't)
//  3. Most recently updated (proxy for activity)
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
	// More orders wins outright
	if candidate.OrdersCount != current.OrdersCount {
		return candidate.OrdersCount > current.OrdersCount
	}
	// Having an email breaks ties
	candidateHasEmail := candidate.Email != ""
	currentHasEmail := current.Email != ""
	if candidateHasEmail != currentHasEmail {
		return candidateHasEmail
	}
	// Recency as final tiebreaker
	return candidate.UpdatedAt.After(current.UpdatedAt)
}
