package intelligence

import (
	"encoding/json"
	"fmt"
	"strings"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// ReasonItem is a prioritized human-readable explanation for a confidence score.
// Importance drives how the UI renders the reason (colour, weight, order).
type ReasonItem struct {
	Text       string `json:"text"`
	Importance string `json:"importance"` // "high" | "medium" | "low"
}

// ConflictItem describes a structural incompatibility between customer records.
// Blocking=true means the conflict prevents both bulk and manual merging.
// Resolvable=true means the user can address it via the Merge Composer field
// selections (e.g. pick which phone number to keep); Resolvable=false on a
// blocking conflict means it is a hard stop regardless of user action.
type ConflictItem struct {
	Type      string `json:"type"`
	Severity  string `json:"severity"`  // "high" | "medium" | "low"
	Blocking  bool   `json:"blocking"`
	Resolvable bool  `json:"resolvable"`
}

// ConflictResult holds structural conflicts found in a customer cluster.
type ConflictResult struct {
	Conflicts []ConflictItem
	// Severity is the highest severity across all conflicts: "high", "medium", "low", or "".
	Severity string
}

// GenerateBreakdownReasons converts per-field similarity scores (0–1) into
// prioritized human-readable explanations for the confidence breakdown UI.
func GenerateBreakdownReasons(emailSim, nameSim, phoneSim, addrSim float64) []ReasonItem {
	var reasons []ReasonItem

	// Name — high importance when decisive
	switch {
	case nameSim >= 0.97:
		reasons = append(reasons, ReasonItem{Text: "Exact name match", Importance: "high"})
	case nameSim >= 0.82:
		reasons = append(reasons, ReasonItem{Text: "Strong name match", Importance: "high"})
	case nameSim >= 0.60:
		reasons = append(reasons, ReasonItem{Text: "Partial name match", Importance: "medium"})
	default:
		reasons = append(reasons, ReasonItem{Text: "Names differ significantly", Importance: "high"})
	}

	// Email
	switch {
	case emailSim >= 0.99:
		reasons = append(reasons, ReasonItem{Text: "Identical email addresses", Importance: "high"})
	case emailSim >= 0.50:
		reasons = append(reasons, ReasonItem{Text: "Same email domain", Importance: "medium"})
	case emailSim > 0.10:
		reasons = append(reasons, ReasonItem{Text: "Similar email addresses", Importance: "medium"})
	default:
		reasons = append(reasons, ReasonItem{Text: "Different email domains", Importance: "medium"})
	}

	// Phone — only mention when there is data
	if phoneSim >= 0.99 {
		reasons = append(reasons, ReasonItem{Text: "Identical phone numbers", Importance: "high"})
	} else if phoneSim >= 0.75 {
		reasons = append(reasons, ReasonItem{Text: "Similar phone numbers", Importance: "medium"})
	} else if phoneSim > 0 {
		reasons = append(reasons, ReasonItem{Text: "Partial phone match", Importance: "low"})
	}

	// Address — only mention when there is data
	if addrSim >= 0.95 {
		reasons = append(reasons, ReasonItem{Text: "Same shipping address", Importance: "high"})
	} else if addrSim >= 0.70 {
		reasons = append(reasons, ReasonItem{Text: "Similar shipping address", Importance: "medium"})
	} else if addrSim > 0 {
		reasons = append(reasons, ReasonItem{Text: "Partial address match", Importance: "low"})
	}

	return reasons
}

// GenerateSummary produces a one-line explanation of the confidence score
// suitable for surfacing at the top of the merge review UI.
func GenerateSummary(reasons []ReasonItem, conflicts []ConflictItem, confidence float64) string {
	// Collect the most important positive signals.
	var highReasons []string
	for _, r := range reasons {
		if r.Importance == "high" && !strings.Contains(strings.ToLower(r.Text), "differ") &&
			!strings.Contains(strings.ToLower(r.Text), "different") {
			highReasons = append(highReasons, strings.ToLower(r.Text))
		}
	}

	hasBlockingConflict := false
	for _, c := range conflicts {
		if c.Blocking {
			hasBlockingConflict = true
			break
		}
	}

	switch {
	case hasBlockingConflict:
		return "Accounts share some identifying data but have blocking conflicts — manual review required before merging."
	case confidence >= 0.90 && len(highReasons) >= 2:
		return fmt.Sprintf("Very likely the same customer based on %s and %s.", highReasons[0], highReasons[1])
	case confidence >= 0.90 && len(highReasons) == 1:
		return fmt.Sprintf("Very likely the same customer based on %s.", highReasons[0])
	case confidence >= 0.75 && len(highReasons) >= 1:
		return fmt.Sprintf("Likely the same customer based on %s, though some fields differ — verify before merging.", highReasons[0])
	case confidence >= 0.50:
		return "Possible duplicate — some fields match, but not enough to be certain. Review carefully."
	default:
		return "Low confidence match — these may be different customers. Proceed with caution."
	}
}

// DetectConflicts inspects raw field values across all customers in the cluster
// for structural conflicts that should override or inform risk classification.
func DetectConflicts(customers []models.CustomerCache) ConflictResult {
	if len(customers) < 2 {
		return ConflictResult{}
	}

	var conflicts []ConflictItem
	maxSev := 0 // 1=low 2=medium 3=high

	add := func(item ConflictItem) {
		conflicts = append(conflicts, item)
		sev := 0
		switch item.Severity {
		case "high":
			sev = 3
		case "medium":
			sev = 2
		case "low":
			sev = 1
		}
		if sev > maxSev {
			maxSev = sev
		}
	}

	// ── Country mismatch — hard stop: different people, cannot be field-resolved ──
	countries := uniqueFieldValues(customers, func(c models.CustomerCache) string {
		return extractCountryFromCache(c)
	})
	if len(countries) > 1 {
		add(ConflictItem{Type: "different_countries", Severity: "high", Blocking: true, Resolvable: false})
	}

	// ── Last name mismatch — resolvable via Merge Composer name selection ──
	lastNames := uniqueFieldValues(customers, func(c models.CustomerCache) string {
		parts := strings.Fields(c.Name)
		if len(parts) == 0 {
			return ""
		}
		return strings.ToLower(parts[len(parts)-1])
	})
	if len(lastNames) > 1 {
		add(ConflictItem{Type: "different_last_names", Severity: "medium", Blocking: false, Resolvable: true})
	}

	// ── Disabled / blocked account — hard stop ──
	for _, c := range customers {
		if strings.EqualFold(strings.TrimSpace(c.State), "disabled") {
			add(ConflictItem{Type: "disabled_account", Severity: "high", Blocking: true, Resolvable: false})
			break
		}
	}

	// ── Phone country code mismatch — resolvable by picking which phone to keep ──
	phoneCodes := uniqueFieldValues(customers, func(c models.CustomerCache) string {
		return extractPhoneCountryCode(utils.NormalizePhone(c.Phone))
	})
	if len(phoneCodes) > 1 {
		add(ConflictItem{Type: "different_phone_country_codes", Severity: "medium", Blocking: false, Resolvable: true})
	}

	// ── Order country mismatch — non-blocking when there is a hard identity anchor ──
	if orderCountryMismatch(customers) && !hasExactIdentityAnchor(customers) {
		add(ConflictItem{Type: "order_country_mismatch", Severity: "high", Blocking: false, Resolvable: false})
	}

	// ── Risk / fraud tags — hard stop: cannot be resolved by field selection ──
	riskTags := []string{"fraud", "chargeback", "blocked", "disputed", "high_risk"}
	for _, c := range customers {
		for _, tag := range c.Tags {
			for _, risk := range riskTags {
				if strings.EqualFold(strings.TrimSpace(tag), risk) {
					add(ConflictItem{
						Type:       "risk_tag:" + strings.ToLower(tag),
						Severity:   "high",
						Blocking:   true,
						Resolvable: false,
					})
				}
			}
		}
	}

	severity := ""
	switch maxSev {
	case 3:
		severity = "high"
	case 2:
		severity = "medium"
	case 1:
		severity = "low"
	}

	return ConflictResult{Conflicts: conflicts, Severity: severity}
}

// uniqueFieldValues extracts deduplicated non-empty values from customers using fn.
func uniqueFieldValues(customers []models.CustomerCache, fn func(models.CustomerCache) string) []string {
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

// orderCountryMismatch returns true when order addresses across the cluster span
// multiple countries — a signal that these may be different people.
func orderCountryMismatch(members []models.CustomerCache) bool {
	countries := make(map[string]struct{})
	for _, m := range members {
		if len(m.OrderAddresses) == 0 {
			continue
		}
		var addrs []models.OrderAddress
		if err := json.Unmarshal(m.OrderAddresses, &addrs); err != nil {
			continue
		}
		for _, a := range addrs {
			if a.Country != "" {
				countries[strings.ToUpper(a.Country)] = struct{}{}
			}
		}
	}
	return len(countries) > 1
}

// hasExactIdentityAnchor returns true when any two members share an exact email
// or phone match. Used to suppress geographic conflicts for strong identity matches.
func hasExactIdentityAnchor(members []models.CustomerCache) bool {
	for i := 0; i < len(members); i++ {
		for j := i + 1; j < len(members); j++ {
			if members[i].Email != "" && strings.EqualFold(members[i].Email, members[j].Email) {
				return true
			}
			if members[i].Phone != "" && members[i].Phone == members[j].Phone {
				return true
			}
		}
	}
	return false
}

// extractCountryFromCache parses the country field from the customer's address JSON.
func extractCountryFromCache(c models.CustomerCache) string {
	if len(c.AddressJSON) == 0 {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal(c.AddressJSON, &m); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m["country"]))
}

// extractPhoneCountryCode returns a coarse country prefix (+1, +44, +61 etc.)
// from a normalized phone number. Returns "" if the number isn't E.164.
func extractPhoneCountryCode(normalized string) string {
	if !strings.HasPrefix(normalized, "+") {
		return ""
	}
	digits := strings.TrimPrefix(normalized, "+")
	if len(digits) == 0 {
		return ""
	}
	end := 3
	if len(digits) < end {
		end = len(digits)
	}
	return "+" + digits[:end]
}
