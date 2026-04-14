package identity

import (
	"encoding/json"
	"math"
	"strings"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// Score represents pairwise similarity between two customers.
// EmailSim, NameSim, PhoneSim, and AddressSim are 0–1 values used for the
// UI breakdown. Combined is produced by the rule-based confidence engine
// (not a weighted average of the components).
type Score struct {
	EmailSim   float64
	NameSim    float64
	PhoneSim   float64
	AddressSim float64
	Combined   float64
}

// Signals holds discrete binary identity signals extracted from a customer pair.
// The rule-based engine operates on these signals rather than continuous scores,
// which eliminates the "many weak signals add up to a false positive" failure mode.
type Signals struct {
	// Email
	EmailExact       bool // normalized emails are identical
	EmailLocalMatch  bool // same local part (localSim ≥ 0.92), different domain
	EmailDomainMatch bool // same email domain, different local part

	// Phone
	PhoneExact  bool // digit strings are identical
	PhoneSuffix bool // one is a suffix of the other (country-code prefix difference)

	// Name
	NameHigh   bool    // Jaro-Winkler ≥ 0.92 ("John Smith" / "Jon Smith")
	NameMedium bool    // Jaro-Winkler ≥ 0.85 ("John Smith" / "Jane Smith")
	NameSim    float64 // raw value kept for debug logging

	// Address
	AddressExact   bool // full canonical address string is identical
	AddressPartial bool // levenshteinSim ≥ 0.70

	// Blocker
	SameCountry bool // false only when BOTH customers have country data that differs
}

// ScorePair computes a pairwise Score between two cached customers.
// Per-field sims (EmailSim etc.) are continuous 0–1 values for the UI breakdown.
// Combined is produced by the rule-based computeConfidence engine.
func ScorePair(a, b *models.CustomerCache) Score {
	s := Score{}

	emailA := utils.NormalizeEmail(a.Email)
	emailB := utils.NormalizeEmail(b.Email)
	s.EmailSim = emailSimilarity(emailA, emailB)

	nameA := utils.NormalizeName(a.Name)
	nameB := utils.NormalizeName(b.Name)
	s.NameSim = jaroWinkler(nameA, nameB)

	phoneA := utils.NormalizePhone(a.Phone)
	phoneB := utils.NormalizePhone(b.Phone)
	if phoneA != "" && phoneB != "" {
		if phoneA == phoneB {
			s.PhoneSim = 1.0
		} else if strings.HasSuffix(phoneA, phoneB) || strings.HasSuffix(phoneB, phoneA) {
			s.PhoneSim = 0.8
		}
	}

	s.AddressSim = addressSimilarity(a, b)

	// Rule-based confidence replaces the old weighted average.
	// This prevents "many weak signals summing to a false positive".
	sig := extractSignals(emailA, emailB, phoneA, phoneB, s.NameSim, s.AddressSim, a, b)
	s.Combined = computeConfidence(sig)

	return s
}

// extractSignals converts raw field values and pre-computed similarities into
// the discrete signal set used by computeConfidence.
func extractSignals(
	emailA, emailB string,
	phoneA, phoneB string,
	nameSim, addrSim float64,
	a, b *models.CustomerCache,
) Signals {
	var s Signals

	// Country check — only block when both customers have explicit country data.
	s.SameCountry = true
	cA := addressField(a, "country")
	cB := addressField(b, "country")
	if cA != "" && cB != "" && cA != cB {
		s.SameCountry = false
	}

	// Email signals
	if emailA != "" && emailB != "" {
		if emailA == emailB {
			s.EmailExact = true
		} else {
			pA := strings.SplitN(emailA, "@", 2)
			pB := strings.SplitN(emailB, "@", 2)
			if len(pA) == 2 && len(pB) == 2 {
				localSim := levenshteinSim(pA[0], pB[0])
				if pA[1] == pB[1] {
					s.EmailDomainMatch = true
				} else if localSim >= 0.92 {
					s.EmailLocalMatch = true
				}
			}
		}
	}

	// Phone signals
	if phoneA != "" && phoneB != "" {
		if phoneA == phoneB {
			s.PhoneExact = true
		} else if strings.HasSuffix(phoneA, phoneB) || strings.HasSuffix(phoneB, phoneA) {
			s.PhoneSuffix = true
		}
	}

	// Name signals
	s.NameSim = nameSim
	s.NameHigh = nameSim >= 0.92
	s.NameMedium = nameSim >= 0.85

	// Address signals
	s.AddressExact = addrSim >= 0.99
	s.AddressPartial = addrSim >= 0.70

	return s
}

// computeConfidence translates the discrete signal set into a final confidence
// score using an explicit rule table rather than a weighted average.
//
// Design principles:
//   - A single hard-identity signal (exact email, exact phone) is sufficient on its own.
//   - All other cases require two corroborating signals from different field types.
//   - Name alone is never sufficient — it must be combined with a second signal.
//   - Different countries is a hard blocker regardless of other signals.
func computeConfidence(s Signals) float64 {
	// Hard blocker — different countries means different people.
	if !s.SameCountry {
		return 0
	}

	// ── Tier 1: hard identity signals (single signal is definitive) ──────────
	if s.EmailExact {
		return 0.98
	}
	if s.PhoneExact {
		return 0.98
	}

	// ── Tier 2: strong corroborating pairs ───────────────────────────────────
	if s.AddressExact && s.NameHigh {
		return 0.92
	}
	if s.PhoneSuffix && s.NameHigh {
		return 0.90
	}
	if s.EmailLocalMatch && s.NameHigh {
		return 0.88
	}
	if s.AddressExact && s.NameMedium {
		return 0.85
	}
	if s.PhoneSuffix && s.NameMedium {
		return 0.82
	}
	if s.EmailLocalMatch && s.NameMedium {
		return 0.80
	}

	// ── Tier 3: name + weaker contextual signal ───────────────────────────────
	if s.NameHigh && s.AddressPartial {
		return 0.76
	}
	if s.NameHigh && s.EmailDomainMatch {
		return 0.70
	}

	// ── Tier 4: below clustering threshold — surface for review, never auto-cluster ──
	if s.NameMedium && s.AddressExact {
		return 0.65
	}

	// Insufficient signal — name alone is not enough.
	return 0.0
}

// addressSimilarity compares the primary address fields of two customers.
func addressSimilarity(a, b *models.CustomerCache) float64 {
	addrA := extractAddress(a)
	addrB := extractAddress(b)
	if addrA == "" || addrB == "" {
		return 0
	}
	if addrA == addrB {
		return 1.0
	}
	return levenshteinSim(addrA, addrB)
}

// extractAddress parses the address JSON and returns a canonical normalized
// string built from address1+city+zip so that map key ordering differences
// (Go map marshaling is non-deterministic) don't cause false mismatches.
func extractAddress(c *models.CustomerCache) string {
	if len(c.AddressJSON) == 0 {
		return ""
	}
	raw := strings.TrimSpace(string(c.AddressJSON))
	if raw == "{}" || raw == "null" || raw == "" {
		return ""
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return strings.ToLower(raw)
	}

	normalize := func(s string) string {
		return strings.ToLower(strings.TrimSpace(s))
	}

	address1 := normalize(m["address1"])
	city := normalize(m["city"])
	zip := normalize(m["zip"])
	province := normalize(m["province"])
	country := normalize(m["country"])

	if address1 == "" && city == "" && zip == "" {
		return ""
	}

	return address1 + "|" + city + "|" + zip + "|" + province + "|" + country
}

// addressField extracts a single named field from the customer's address JSON.
func addressField(c *models.CustomerCache, field string) string {
	if len(c.AddressJSON) == 0 {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal(c.AddressJSON, &m); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m[field]))
}

// emailSimilarity returns a 0–1 UI score for two normalized email addresses.
// For clustering, the Signals struct + computeConfidence is used instead.
//
// Cross-domain emails are scored on local-part similarity only, capped much
// lower than same-domain pairs. This prevents the old false positive where
// "john@gmail.com" / "john@yahoo.com" scored 0.69 because the full strings
// share the ".com" suffix in the Levenshtein distance.
func emailSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1.0
	}
	pA := strings.SplitN(a, "@", 2)
	pB := strings.SplitN(b, "@", 2)
	if len(pA) != 2 || len(pB) != 2 {
		return levenshteinSim(a, b)
	}
	localA, domainA := pA[0], pA[1]
	localB, domainB := pB[0], pB[1]

	if domainA == domainB {
		// Same domain: compare local parts only — full-string levenshtein inflates
		// scores because the "@domain.com" suffix is identical.
		return 0.8 * levenshteinSim(localA, localB)
	}

	// Different domains: only award partial credit when local parts are very similar
	// (likely the same person using two providers, e.g. john@gmail vs john@company).
	localSim := levenshteinSim(localA, localB)
	switch {
	case localSim >= 0.92:
		return 0.30
	case localSim >= 0.75:
		return 0.15
	default:
		return 0.0
	}
}

// levenshteinSim returns 1 - (edit_distance / max_length).
func levenshteinSim(a, b string) float64 {
	d := levenshtein(a, b)
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	if max == 0 {
		return 1.0
	}
	return 1.0 - float64(d)/float64(max)
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			dp[i][j] = min3(dp[i-1][j]+1, dp[i][j-1]+1, dp[i-1][j-1]+cost)
		}
	}
	return dp[la][lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// jaroWinkler computes the Jaro-Winkler similarity between two strings.
func jaroWinkler(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	jaro := jaroSim(a, b)
	// Winkler prefix bonus (up to 4 chars)
	prefix := 0
	for i := 0; i < len(a) && i < len(b) && i < 4; i++ {
		if a[i] == b[i] {
			prefix++
		} else {
			break
		}
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}

func jaroSim(a, b string) float64 {
	la, lb := len(a), len(b)
	matchDist := int(math.Max(float64(la), float64(lb))/2) - 1
	if matchDist < 0 {
		matchDist = 0
	}

	aMatched := make([]bool, la)
	bMatched := make([]bool, lb)
	matches := 0

	for i := 0; i < la; i++ {
		start := i - matchDist
		if start < 0 {
			start = 0
		}
		end := i + matchDist + 1
		if end > lb {
			end = lb
		}
		for j := start; j < end; j++ {
			if !bMatched[j] && a[i] == b[j] {
				aMatched[i] = true
				bMatched[j] = true
				matches++
				break
			}
		}
	}

	if matches == 0 {
		return 0.0
	}

	transpositions := 0
	k := 0
	for i := 0; i < la; i++ {
		if aMatched[i] {
			for !bMatched[k] {
				k++
			}
			if a[i] != b[k] {
				transpositions++
			}
			k++
		}
	}

	m := float64(matches)
	return (m/float64(la) + m/float64(lb) + (m-float64(transpositions)/2)/m) / 3
}
