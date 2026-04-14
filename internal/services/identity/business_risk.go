package identity

import (
	"time"

	"merger/backend/internal/models"
)

// BusinessRisk summarises the commercial risk of merging a cluster.
//
// Identity confidence answers "are these the same person?"
// Business risk answers "even if they are, how much could go wrong?"
//
// High business risk does not block a merge — it forces the risk level to
// "review" so a human confirms before proceeding. The cost of a wrong merge
// is proportional to the value of the accounts being combined.
type BusinessRisk struct {
	Level       string  // "high" | "medium" | "low" | ""
	ImpactScore float64 // cluster_size × avg_customer_value (blast radius)
	MaxSpend    float64 // highest total_spent in cluster
	SpendDelta  float64 // max − min total_spent
	MaxOrders   int     // highest order count in cluster
	OrderDelta  int     // max − min order count
}

// ComputeBusinessRisk evaluates the commercial risk of merging a set of
// customer accounts. Three signal families:
//
//   - Spend disparity: merging a $2,000 account with a $0 ghost account
//     risks corrupting a high-value customer record.
//   - Order count disparity: a 50-order customer vs a 0-order new account
//     are statistically unlikely to be the same person.
//   - Account age gap: accounts created years apart at high spend are
//     suspicious — may represent different people or life stages.
//
// ImpactScore (cluster_size × avg_value) drives the blast-radius guardrail:
// even a clean identity match requires manual confirmation when the combined
// value at stake exceeds the threshold.
func ComputeBusinessRisk(members []models.CustomerCache) BusinessRisk {
	if len(members) == 0 {
		return BusinessRisk{}
	}

	spends := make([]float64, len(members))
	totalSpend := 0.0
	maxOrders := members[0].OrdersCount
	minOrders := members[0].OrdersCount
	var oldestTime, newestTime *time.Time

	for i, c := range members {
		s := parseSpent(c.TotalSpent) // defined in anchor.go
		spends[i] = s
		totalSpend += s

		if c.OrdersCount > maxOrders {
			maxOrders = c.OrdersCount
		}
		if c.OrdersCount < minOrders {
			minOrders = c.OrdersCount
		}

		if c.ShopifyCreatedAt != nil {
			t := *c.ShopifyCreatedAt
			if oldestTime == nil || t.Before(*oldestTime) {
				oldestTime = &t
			}
			if newestTime == nil || t.After(*newestTime) {
				newestTime = &t
			}
		}
	}

	maxSpend, minSpend := spends[0], spends[0]
	for _, s := range spends[1:] {
		if s > maxSpend {
			maxSpend = s
		}
		if s < minSpend {
			minSpend = s
		}
	}

	avgSpend := totalSpend / float64(len(members))
	impactScore := float64(len(members)) * avgSpend
	spendDelta := maxSpend - minSpend
	orderDelta := maxOrders - minOrders

	ageGapMonths := 0.0
	if oldestTime != nil && newestTime != nil {
		ageGapMonths = newestTime.Sub(*oldestTime).Hours() / 730 // 730 h ≈ 1 month
	}

	level := computeBusinessRiskLevel(maxSpend, spendDelta, orderDelta, ageGapMonths)
	return BusinessRisk{
		Level:       level,
		ImpactScore: impactScore,
		MaxSpend:    maxSpend,
		SpendDelta:  spendDelta,
		MaxOrders:   maxOrders,
		OrderDelta:  orderDelta,
	}
}

// computeBusinessRiskLevel maps business risk signals to a severity string.
func computeBusinessRiskLevel(maxSpend, spendDelta float64, orderDelta int, ageGapMonths float64) string {
	// High: merging a high-value account with a very different history
	if maxSpend >= 1000 && spendDelta >= 500 {
		return "high"
	}
	if orderDelta >= 20 && maxSpend >= 200 {
		return "high"
	}

	// Medium: notable disparity — worth a human glance
	if maxSpend >= 500 && spendDelta >= 200 {
		return "medium"
	}
	if orderDelta >= 10 && maxSpend >= 100 {
		return "medium"
	}
	if ageGapMonths >= 36 && maxSpend >= 100 {
		return "medium"
	}

	if maxSpend > 0 {
		return "low"
	}
	return ""
}
