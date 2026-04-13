package intelligence

import (
	"encoding/json"
	"strings"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// GenerateBreakdownReasons converts per-field similarity scores (0–1) into
// human-readable explanations for the confidence breakdown UI.
// Only non-obvious signals are included — zero scores on optional fields
// (phone, address) are omitted so the UI stays clean.
func GenerateBreakdownReasons(emailSim, nameSim, phoneSim, addrSim float64) []string {
	var reasons []string

	// Name
	switch {
	case nameSim >= 0.97:
		reasons = append(reasons, "Exact name match")
	case nameSim >= 0.82:
		reasons = append(reasons, "Strong name match")
	case nameSim >= 0.60:
		reasons = append(reasons, "Partial name match")
	default:
		reasons = append(reasons, "Names differ significantly")
	}

	// Email
	switch {
	case emailSim >= 0.99:
		reasons = append(reasons, "Identical email addresses")
	case emailSim >= 0.50:
		reasons = append(reasons, "Same email domain")
	case emailSim > 0.10:
		reasons = append(reasons, "Similar email addresses")
	default:
		reasons = append(reasons, "Different email domains")
	}

	// Phone — only mention when there is data
	if phoneSim >= 0.99 {
		reasons = append(reasons, "Identical phone numbers")
	} else if phoneSim >= 0.75 {
		reasons = append(reasons, "Similar phone numbers")
	} else if phoneSim > 0 {
		reasons = append(reasons, "Partial phone match")
	}

	// Address — only mention when there is data
	if addrSim >= 0.95 {
		reasons = append(reasons, "Same shipping address")
	} else if addrSim >= 0.70 {
		reasons = append(reasons, "Similar shipping address")
	} else if addrSim > 0 {
		reasons = append(reasons, "Partial address match")
	}

	return reasons
}

// ConflictResult holds structural conflicts found in a customer cluster.
type ConflictResult struct {
	Conflicts []string
	// Severity is "high", "medium", "low", or "" (no conflicts).
	Severity string
}

// DetectConflicts inspects raw field values across all customers in the cluster
// for structural conflicts that should override or inform risk classification.
//
// Unlike similarity scores (which measure resemblance), conflicts measure
// real incompatibilities — things that cannot both be true of the same person.
func DetectConflicts(customers []models.CustomerCache) ConflictResult {
	if len(customers) < 2 {
		return ConflictResult{}
	}

	var conflicts []string
	maxSev := 0 // 1=low 2=medium 3=high

	bump := func(level int) {
		if level > maxSev {
			maxSev = level
		}
	}

	// ── Country mismatch — strong evidence of different people ──
	countries := uniqueFieldValues(customers, func(c models.CustomerCache) string {
		return extractCountryFromCache(c)
	})
	if len(countries) > 1 {
		conflicts = append(conflicts, "different_countries")
		bump(3)
	}

	// ── Last name mismatch ──
	// Name changes happen (marriage, legal change) so this is medium, not high.
	lastNames := uniqueFieldValues(customers, func(c models.CustomerCache) string {
		parts := strings.Fields(c.Name)
		if len(parts) == 0 {
			return ""
		}
		return strings.ToLower(parts[len(parts)-1])
	})
	if len(lastNames) > 1 {
		conflicts = append(conflicts, "different_last_names")
		bump(2)
	}

	// ── Disabled / blocked account ──
	for _, c := range customers {
		if strings.EqualFold(strings.TrimSpace(c.State), "disabled") {
			conflicts = append(conflicts, "disabled_account")
			bump(3)
			break
		}
	}

	// ── Phone country code mismatch ──
	phoneCodes := uniqueFieldValues(customers, func(c models.CustomerCache) string {
		return extractPhoneCountryCode(utils.NormalizePhone(c.Phone))
	})
	if len(phoneCodes) > 1 {
		conflicts = append(conflicts, "different_phone_country_codes")
		bump(2)
	}

	// ── Risk / fraud tags ──
	riskTags := []string{"fraud", "chargeback", "blocked", "disputed", "high_risk"}
	for _, c := range customers {
		for _, tag := range c.Tags {
			for _, risk := range riskTags {
				if strings.EqualFold(strings.TrimSpace(tag), risk) {
					conflicts = append(conflicts, "risk_tag:"+strings.ToLower(tag))
					bump(3)
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
	// Use up to 3 digits after "+" — sufficient to distinguish countries.
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
