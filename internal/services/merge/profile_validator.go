package merge

import (
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
// returns a split result. The FieldSelection parameter is recorded for context
// but hard blockers are always determined by the raw customer data.
//
// overrideDisabled allows the caller to bypass the disabled_account block when
// the user has explicitly acknowledged the risk. All other hard blocks (fraud
// tags, different country, etc.) remain enforced regardless.
func ValidateFinalProfile(customers []models.CustomerCache, _ FieldSelection, overrideDisabled bool) ProfileValidationResult {
	result := intelligence.DetectConflicts(customers)

	var blocking, resolvable []intelligence.ConflictItem
	for _, c := range result.Conflicts {
		isHardBlock := c.Blocking && !c.Resolvable
		// disabled_account is overridable when the user has explicitly acknowledged it.
		if isHardBlock && c.Type == "disabled_account" && overrideDisabled {
			isHardBlock = false
		}
		if isHardBlock {
			blocking = append(blocking, c)
		} else {
			resolvable = append(resolvable, c)
		}
	}

	return ProfileValidationResult{
		HasBlockingConflicts: len(blocking) > 0,
		BlockingConflicts:    blocking,
		ResolvableConflicts:  resolvable,
		IsReadyToMerge:       len(blocking) == 0,
	}
}
