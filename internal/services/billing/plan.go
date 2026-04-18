// Package billing defines subscription plans, feature gates, and usage limits
// for the Customer Harmony app.
//
// Plans:
//   - free  — 1,000 customers, 10 merges/month, profile matching only
//   - basic — 15,000 customers, unlimited merges, order intelligence + history + snapshots
//   - pro   — 100,000 customers, everything + auto-detect + bulk merge + restore
package billing

import "errors"

// Plan identifiers — stored in merchant_settings.plan.
const (
	PlanFree  = "free"
	PlanBasic = "basic"
	PlanPro   = "pro"
)

// Pricing in USD/month (used in appSubscriptionCreate).
const (
	PriceBasic = 12.00
	PricePro   = 29.00
)

// Customer scan limits per plan.
const (
	CustomerLimitFree  = 1_000
	CustomerLimitBasic = 15_000
	CustomerLimitPro   = 100_000
)

// Monthly merge limits (-1 = unlimited).
const (
	MergeLimitFree  = 10
	MergeLimitBasic = -1
	MergeLimitPro   = -1
)

// Feature identifiers for gate checks.
const (
	FeatureOrderIntelligence = "order_intelligence" // behavioral signals (basic+)
	FeatureMergeHistory      = "merge_history"      // audit log (basic+)
	FeatureSnapshots         = "snapshots"          // snapshot creation (basic+)
	FeatureOverride          = "override"           // disabled-account override (basic+)
	FeatureAutoDetect        = "auto_detect"        // scheduled scans (pro only)
	FeatureBulkMerge         = "bulk_merge"         // bulk merge tools (pro only)
	FeatureRestore           = "restore"            // snapshot restore workflow (pro only)
)

// ErrPlanLimitReached is returned when a usage limit blocks the operation.
var ErrPlanLimitReached = errors.New("plan limit reached — upgrade to continue")

// ErrFeatureNotAvailable is returned when a feature is not included in the plan.
var ErrFeatureNotAvailable = errors.New("feature not available on current plan")

// PlanInfo holds the display metadata for a plan.
type PlanInfo struct {
	Name          string
	PriceUSD      float64 // 0 = free
	CustomerLimit int
	MergeLimit    int // -1 = unlimited
}

// Plans returns the full plan catalogue.
func Plans() map[string]PlanInfo {
	return map[string]PlanInfo{
		PlanFree:  {Name: "Free", PriceUSD: 0, CustomerLimit: CustomerLimitFree, MergeLimit: MergeLimitFree},
		PlanBasic: {Name: "Basic", PriceUSD: PriceBasic, CustomerLimit: CustomerLimitBasic, MergeLimit: MergeLimitBasic},
		PlanPro:   {Name: "Pro", PriceUSD: PricePro, CustomerLimit: CustomerLimitPro, MergeLimit: MergeLimitPro},
	}
}

// CustomerLimit returns the maximum number of customers a plan can scan.
func CustomerLimit(plan string) int {
	switch plan {
	case PlanBasic:
		return CustomerLimitBasic
	case PlanPro:
		return CustomerLimitPro
	default:
		return CustomerLimitFree
	}
}

// MergeLimit returns the monthly merge cap for a plan (-1 = unlimited).
func MergeLimit(plan string) int {
	switch plan {
	case PlanBasic, PlanPro:
		return -1
	default:
		return MergeLimitFree
	}
}

// IsFeatureEnabled returns true if the given feature is available on the plan.
func IsFeatureEnabled(plan, feature string) bool {
	switch feature {
	case FeatureOrderIntelligence, FeatureMergeHistory, FeatureSnapshots, FeatureOverride:
		return plan == PlanBasic || plan == PlanPro
	case FeatureAutoDetect, FeatureBulkMerge, FeatureRestore:
		return plan == PlanPro
	}
	return false
}

// CheckMergeAllowed returns ErrPlanLimitReached if the free-tier monthly merge
// cap has been hit. On paid plans it always returns nil.
func CheckMergeAllowed(plan string, mergesThisMonth int) error {
	limit := MergeLimit(plan)
	if limit == -1 {
		return nil // unlimited
	}
	if mergesThisMonth >= limit {
		return ErrPlanLimitReached
	}
	return nil
}

// CheckCustomerLimit returns ErrPlanLimitReached if the customer count exceeds
// the plan's scanning limit.
func CheckCustomerLimit(plan string, customerCount int) error {
	if customerCount > CustomerLimit(plan) {
		return ErrPlanLimitReached
	}
	return nil
}
