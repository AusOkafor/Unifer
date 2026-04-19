package merge

import (
	"strings"

	"merger/backend/internal/models"
	"merger/backend/internal/services/intelligence"
)

// FieldSelection maps field names to the customer source the user chose.
// Values: "primary" | "secondary" | "" (not yet selected).
type FieldSelection struct {
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	Address string `json:"address"`
	Name    string `json:"name"`
}

// ConflictSettings carries the merchant's per-setting overrides into
// the profile validator. Defaults (zero-value) are the safe/blocking direction.
type ConflictSettings struct {
	// OverrideDisabled bypasses the disabled_account hard block when the user
	// has explicitly acknowledged the reactivation risk.
	OverrideDisabled bool
	// BlockFraudTags: when false, risk_tag conflicts are non-blocking.
	BlockFraudTags bool
	// BlockDifferentCountry: when false, different_countries conflicts are non-blocking.
	BlockDifferentCountry bool
}

// ProfileValidationResult is returned by ValidateFinalProfile.
// It splits conflicts into hard stops and user-resolvable items so the UI
// can render three distinct states: BLOCKED / NEEDS_RESOLUTION / READY.
type ProfileValidationResult struct {
	// HasBlockingConflicts is true when at least one hard-stop conflict exists.
	// These cannot be resolved via field selection and permanently disable merge.
	HasBlockingConflicts bool `json:"has_blocking_conflicts"`

	// BlockingConflicts are conflicts the user cannot resolve (different country,
	// fraud/chargeback tags, disabled accounts).
	BlockingConflicts []intelligence.ConflictItem `json:"blocking_conflicts"`

	// ResolvableConflicts are conflicts the user CAN address via the Merge Composer
	// (different last name, different phone country code, etc.).
	ResolvableConflicts []intelligence.ConflictItem `json:"resolvable_conflicts"`

	// IsReadyToMerge is true when there are no hard-stop blocking conflicts.
	// Resolvable conflicts remaining is acceptable for manual merges — the user
	// has explicitly reviewed and composed the final profile.
	IsReadyToMerge bool `json:"is_ready_to_merge"`
}

// ValidateFinalProfile checks the given customers for structural conflicts and
// returns a split result. Settings controls which conflict types are treated as
// hard blocks vs. allowed.
func ValidateFinalProfile(customers []models.CustomerCache, _ FieldSelection, s ConflictSettings) ProfileValidationResult {
	result := intelligence.DetectConflicts(customers)

	var blocking, resolvable []intelligence.ConflictItem
	for _, c := range result.Conflicts {
		isHardBlock := c.Blocking && !c.Resolvable

		// disabled_account is overridable when the user explicitly acknowledged it,
		// or when the merchant has turned off the block_disabled_accounts guard.
		if isHardBlock && c.Type == "disabled_account" && s.OverrideDisabled {
			isHardBlock = false
		}
		// Fraud/risk tags: respect the block_fraud_tags setting.
		if isHardBlock && strings.HasPrefix(c.Type, "risk_tag:") && !s.BlockFraudTags {
			isHardBlock = false
		}
		// Country mismatch: respect the block_different_country setting.
		if isHardBlock && c.Type == "different_countries" && !s.BlockDifferentCountry {
			isHardBlock = false
		}

		if isHardBlock {
			blocking = append(blocking, c)
		} else {
			resolvable = append(resolvable, c)
		}
	}

	if blocking == nil {
		blocking = []intelligence.ConflictItem{}
	}
	if resolvable == nil {
		resolvable = []intelligence.ConflictItem{}
	}

	return ProfileValidationResult{
		HasBlockingConflicts: len(blocking) > 0,
		BlockingConflicts:    blocking,
		ResolvableConflicts:  resolvable,
		IsReadyToMerge:       len(blocking) == 0,
	}
}
