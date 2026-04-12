package intelligence

import (
	"sort"
	"strings"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// SimulationPreview describes what a merge would produce without executing it.
// Computed entirely from customer_cache — no Shopify API calls.
type SimulationPreview struct {
	SurvivingCustomerID int64           `json:"surviving_customer_id"`
	TotalOrderCount     int             `json:"total_order_count"`
	MergedTags          []string        `json:"merged_tags"`
	FieldConflicts      []FieldConflict `json:"field_conflicts"`
}

// FieldConflict describes a field with different values across group members.
type FieldConflict struct {
	Field  string   `json:"field"`
	Values []string `json:"values"` // one entry per customer that has the field
}

// buildSimulation produces a SimulationPreview for the given customer cluster.
func buildSimulation(customers []models.CustomerCache, primaryID int64) SimulationPreview {
	// Total combined order count
	totalOrders := 0
	for _, c := range customers {
		totalOrders += c.OrdersCount
	}

	// Merge all tags (deduplicated, sorted)
	tagSet := make(map[string]struct{})
	for _, c := range customers {
		for _, t := range c.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tagSet[t] = struct{}{}
			}
		}
	}
	mergedTags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		mergedTags = append(mergedTags, t)
	}
	sort.Strings(mergedTags)

	// Detect field conflicts
	var conflicts []FieldConflict

	// Email conflict: collect normalized unique emails
	emails := uniqueValues(customers, func(c models.CustomerCache) string {
		return utils.NormalizeEmail(c.Email)
	})
	if len(emails) > 1 {
		conflicts = append(conflicts, FieldConflict{Field: "email", Values: emails})
	}

	// Phone conflict: collect normalized unique phones
	phones := uniqueValues(customers, func(c models.CustomerCache) string {
		return utils.NormalizePhone(c.Phone)
	})
	if len(phones) > 1 {
		conflicts = append(conflicts, FieldConflict{Field: "phone", Values: phones})
	}

	return SimulationPreview{
		SurvivingCustomerID: primaryID,
		TotalOrderCount:     totalOrders,
		MergedTags:          mergedTags,
		FieldConflicts:      conflicts,
	}
}

// uniqueValues extracts non-empty values from customers using fn, deduplicating them.
func uniqueValues(customers []models.CustomerCache, fn func(models.CustomerCache) string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, c := range customers {
		v := fn(c)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
