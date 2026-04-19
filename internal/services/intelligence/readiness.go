package intelligence

import (
	"fmt"
	"strings"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// Penalty weights. Penalties are subtracted from 100 to produce ReadinessScore.
const (
	penaltyMissingEmail    = 15.0
	penaltyMissingPhone    = 5.0
	penaltySubscriptionTag = 30.0
	penaltyRiskTag         = 20.0
	penaltyPhoneMismatch   = 10.0
)

// subscriptionTagKeywords are tags that indicate an active subscription — blocking a safe merge.
var subscriptionTagKeywords = []string{
	"subscriber", "subscription", "active_subscription", "vip_subscriber",
	"membership", "active_member",
}

// riskTagKeywords flag customers with financial or fraud concerns.
var riskTagKeywords = []string{
	"chargeback", "fraud", "flagged", "disputed", "high_risk", "blocked",
}

// scoreReadiness computes a 0–100 readiness score plus human-readable
// risk flags and reasoning for the duplicate group.
//
// A score of 100 means no detected risk factors.
// Each penalty deducts from that baseline.
func scoreReadiness(customers []models.CustomerCache, primaryID int64) (score float64, riskFlags, reasoning []string) {
	penalties := 0.0

	// --- Data completeness ---
	for _, c := range customers {
		label := fmt.Sprintf("customer %d", c.ShopifyCustomerID)

		if c.Email == "" {
			penalties += penaltyMissingEmail
			riskFlags = append(riskFlags, label+" is missing an email address")
			reasoning = append(reasoning, fmt.Sprintf("−%.0f pts: %s has no email address", penaltyMissingEmail, label))
		} else {
			reasoning = append(reasoning, label+" has a verified email address")
		}

		if c.Phone == "" {
			penalties += penaltyMissingPhone
			reasoning = append(reasoning, fmt.Sprintf("−%.0f pts: %s has no phone number", penaltyMissingPhone, label))
		} else {
			reasoning = append(reasoning, label+" has a phone number on record")
		}
	}

	// --- Subscription and risk tags ---
	for _, c := range customers {
		label := fmt.Sprintf("customer %d", c.ShopifyCustomerID)
		for _, raw := range c.Tags {
			tag := strings.ToLower(strings.TrimSpace(raw))
			if matchesAny(tag, subscriptionTagKeywords) {
				penalties += penaltySubscriptionTag
				riskFlags = append(riskFlags, label+` has active subscription tag "`+raw+`"`)
				reasoning = append(reasoning, fmt.Sprintf("−%.0f pts: %s has active subscription tag \"%s\"", penaltySubscriptionTag, label, raw))
			}
			if matchesAny(tag, riskTagKeywords) {
				penalties += penaltyRiskTag
				riskFlags = append(riskFlags, label+` has risk tag "`+raw+`"`)
				reasoning = append(reasoning, fmt.Sprintf("−%.0f pts: %s has risk tag \"%s\"", penaltyRiskTag, label, raw))
			}
		}
	}

	// --- Field conflicts ---
	phones := nonEmpty(customers, func(c models.CustomerCache) string {
		return utils.NormalizePhone(c.Phone)
	})
	if len(unique(phones)) > 1 {
		penalties += penaltyPhoneMismatch
		riskFlags = append(riskFlags, "phone numbers differ between customers")
		reasoning = append(reasoning, fmt.Sprintf("−%.0f pts: phone numbers differ between customers", penaltyPhoneMismatch))
	}

	// --- Positive signals ---
	totalOrders := 0
	for _, c := range customers {
		totalOrders += c.OrdersCount
	}
	if totalOrders > 0 {
		reasoning = append(reasoning, fmt.Sprintf("combined order history of %d orders", totalOrders))
	}

	// Note which customer is recommended as primary and why
	for _, c := range customers {
		if c.ShopifyCustomerID == primaryID {
			if c.OrdersCount > 0 {
				reasoning = append(reasoning,
					fmt.Sprintf("recommended primary has %d orders and is the most active account", c.OrdersCount))
			} else {
				reasoning = append(reasoning, "recommended primary selected based on email completeness and recency")
			}
			break
		}
	}

	score = 100.0 - penalties
	if score < 0 {
		score = 0
	}
	return score, riskFlags, reasoning
}

func matchesAny(tag string, keywords []string) bool {
	for _, kw := range keywords {
		if tag == kw {
			return true
		}
	}
	return false
}

func nonEmpty(customers []models.CustomerCache, fn func(models.CustomerCache) string) []string {
	var out []string
	for _, c := range customers {
		if v := fn(c); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func unique(vals []string) []string {
	seen := make(map[string]struct{}, len(vals))
	var out []string
	for _, v := range vals {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
