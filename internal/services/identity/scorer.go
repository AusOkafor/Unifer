package identity

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// Score represents pairwise similarity between two customers.
// EmailSim/NameSim/PhoneSim/AddressSim are continuous 0–1 values for the UI
// breakdown chart. Combined is the rule-based confidence (not a weighted average).
// Sig is carried through for structured observability logging.
type Score struct {
	EmailSim         float64
	NameSim          float64
	PhoneSim         float64
	AddressSim       float64
	Combined         float64
	ConfidenceSource string  // "behavioral" | "profile" | "mixed"
	Sig              Signals
}

// Signals holds discrete binary identity signals extracted from a customer pair.
// The rule-based engine operates on these rather than continuous scores, preventing
// the "many weak signals add up to a false positive" failure mode.
//
// Thresholds:
//   - NameHigh:      Jaro-Winkler ≥ 0.90  ("Jon Smith" / "John Smith")
//   - NameMedium:    Jaro-Winkler ≥ 0.82  ("John Smith" / "Jane Smith")
//   - AddressExact:  levenshteinSim ≥ 0.99
//   - AddressPartial: levenshteinSim ≥ 0.65
type Signals struct {
	// ── Positive signals ──────────────────────────────────────────────────────
	// Email
	EmailExact       bool // normalized emails are identical
	EmailLocalExact  bool // different domain, local parts identical (localSim ≥ 0.99)
	EmailLocalFuzzy  bool // different domain, local parts similar (0.92 ≤ localSim < 0.99)
	EmailDomainMatch bool // same email domain, different local part

	// Phone
	PhoneExact  bool // digit strings are identical
	PhoneSuffix bool // one is a suffix of the other (country-code prefix difference)

	// Name
	NameHigh   bool    // Jaro-Winkler ≥ 0.90
	NameMedium bool    // Jaro-Winkler ≥ 0.82
	NameSim    float64 // raw value for fallback scoring and observability

	// Address
	AddressExact   bool // full canonical address string identical (sim ≥ 0.99)
	AddressPartial bool // levenshteinSim ≥ 0.65

	// ── Blocker ───────────────────────────────────────────────────────────────
	SameCountry bool // false only when BOTH customers have country data that differs

	// ── Behavioral (order-derived) signals ───────────────────────────────────
	OrderAddressExact   bool // any order address city+zip+country match exactly
	OrderAddressPartial bool // any order address levenshteinSim ≥ 0.85
	OrderNameHigh       bool // any order name Jaro-Winkler ≥ 0.92
	RecentOrderOverlap  bool // both customers ordered within 7 days of each other

	// ── Penalty signals ───────────────────────────────────────────────────────
	DifferentLastName    bool // last-name tokens differ (marriage, alias, different person)
	DifferentEmailDomain bool // both have email, domains differ, no local-part match
	PhoneAsymmetry       bool // one customer has a phone number, the other does not

	// ── Negative behavioral signals ──────────────────────────────────────────
	OrderNameConflict    bool // order names on each side that clearly differ (JW ≤ 0.70)
	OrderAddressConflict bool // order addresses from different countries
}

// ScorePair computes a pairwise Score between two cached customers.
// Per-field sims are continuous values for the UI breakdown.
// Combined is produced by the rule-based confidence engine.
// behavioralEnabled gates order-derived early-return rules.
func ScorePair(a, b *models.CustomerCache, behavioralEnabled bool) Score {
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

	// Extract discrete signals, then compute rule-based confidence.
	s.Sig = extractSignals(emailA, emailB, phoneA, phoneB, nameA, nameB, s.NameSim, s.AddressSim, a, b)
	s.Combined, s.ConfidenceSource = computeConfidence(s.Sig, s.EmailSim, s.NameSim, s.PhoneSim, s.AddressSim, behavioralEnabled)

	return s
}

// extractSignals converts raw field values and pre-computed similarities into
// the discrete signal set consumed by the confidence engine.
func extractSignals(
	emailA, emailB string,
	phoneA, phoneB string,
	nameA, nameB string,
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
				} else {
					switch {
					case localSim >= 0.99:
						s.EmailLocalExact = true
					case localSim >= 0.92:
						s.EmailLocalFuzzy = true
					}
				}
			}
		}
		// Penalty: completely different emails (different domain, no local similarity)
		s.DifferentEmailDomain = !s.EmailExact &&
			!s.EmailDomainMatch &&
			!s.EmailLocalExact &&
			!s.EmailLocalFuzzy
	}

	// Phone signals
	if phoneA != "" && phoneB != "" {
		if phoneA == phoneB {
			s.PhoneExact = true
		} else if strings.HasSuffix(phoneA, phoneB) || strings.HasSuffix(phoneB, phoneA) {
			s.PhoneSuffix = true
		}
	}
	// Penalty: phone present on exactly one side
	s.PhoneAsymmetry = (phoneA == "") != (phoneB == "")

	// Name signals
	s.NameSim = nameSim
	s.NameHigh = nameSim >= 0.90
	s.NameMedium = nameSim >= 0.82

	// Penalty: last-name tokens differ
	lastA := lastToken(nameA)
	lastB := lastToken(nameB)
	if lastA != "" && lastB != "" && lastA != lastB {
		s.DifferentLastName = true
	}

	// Address signals
	s.AddressExact = addrSim >= 0.99
	s.AddressPartial = addrSim >= 0.65

	// Behavioral (order-derived) signals
	orderAddrsA := parseOrderAddresses(a.OrderAddresses)
	orderAddrsB := parseOrderAddresses(b.OrderAddresses)
	if len(orderAddrsA) > 0 && len(orderAddrsB) > 0 {
		s.OrderAddressExact = anyExactOrderAddressMatch(orderAddrsA, orderAddrsB)
		s.OrderAddressPartial = anyPartialOrderAddressMatch(orderAddrsA, orderAddrsB)
	}
	if len(a.OrderNames) > 0 && len(b.OrderNames) > 0 {
		s.OrderNameHigh = anyHighOrderNameMatch(a.OrderNames, b.OrderNames)
	}
	if a.LastOrderAt != nil && b.LastOrderAt != nil {
		diff := a.LastOrderAt.Sub(*b.LastOrderAt)
		if diff < 0 {
			diff = -diff
		}
		s.RecentOrderOverlap = diff <= 7*24*time.Hour
	}

	// Negative behavioral signals
	if len(a.OrderNames) > 0 && len(b.OrderNames) > 0 {
		s.OrderNameConflict = anyConflictingOrderNames(a.OrderNames, b.OrderNames)
	}
	if len(orderAddrsA) > 0 && len(orderAddrsB) > 0 {
		s.OrderAddressConflict = anyConflictingOrderCountries(orderAddrsA, orderAddrsB)
	}

	return s
}

// computeConfidence is the top-level confidence function.
// It applies the rule table, falls back to a soft scorer for edge cases,
// then subtracts penalties. Tier-1 hard-identity scores (≥ 0.97) bypass
// penalties — an exact email match is conclusive regardless of other fields.
//
// NOTE (architectural debt): behavioral early-return rules use hardcoded
// confidence tiers. Edge cases that fall between rules get no behavioral
// boost — they drop straight through to the profile rule table. A future
// evolution should add a weighted fallback layer (similar to computeFallback
// for profile signals) that blends behavioral evidence with profile evidence
// rather than forcing a binary match/no-match. Deferred intentionally to
// avoid over-engineering before we have real-world merge outcome data to
// calibrate weights against. Track via: merge_source_counts metrics.
func computeConfidence(sig Signals, emailSim, nameSim, phoneSim, addrSim float64, behavioralEnabled bool) (float64, string) {
	if !sig.SameCountry {
		return 0, ""
	}

	// Behavioral early-return rules — gated behind the feature flag.
	if behavioralEnabled {
		if sig.OrderAddressExact && sig.NameHigh && sig.SameCountry && !sig.DifferentLastName {
			return 0.96, "behavioral"
		}
		if sig.OrderNameHigh && sig.PhoneExact {
			return 0.95, "mixed"
		}
		if sig.OrderAddressExact && sig.EmailLocalExact {
			return 0.94, "mixed"
		}
		if sig.RecentOrderOverlap && sig.OrderAddressPartial && sig.NameHigh {
			return 0.91, "behavioral"
		}
		if sig.OrderAddressPartial && sig.NameHigh {
			return 0.90, "mixed"
		}

		log.Debug().
			Bool("order_address_exact", sig.OrderAddressExact).
			Bool("order_address_partial", sig.OrderAddressPartial).
			Bool("order_name_high", sig.OrderNameHigh).
			Bool("recent_order_overlap", sig.RecentOrderOverlap).
			Bool("different_last_name", sig.DifferentLastName).
			Msg("behavioral rule evaluation")
	}

	base := computeBaseRules(sig)

	// Soft fallback: engages when no rule matched but there is a medium-or-better
	// name signal combined with partial address or domain evidence. This prevents
	// the system from being completely blind to edge cases that fall just outside
	// the discrete signal boundaries (e.g. nameSim=0.91, addrSim=0.68).
	if base == 0 {
		base = computeFallback(sig, nameSim, addrSim, emailSim, phoneSim)
	}

	if base == 0 {
		return 0, ""
	}

	// Tier-1 hard-identity scores are not penalised — they are conclusive.
	if base >= 0.97 {
		return base, "profile"
	}

	// Apply penalty signals.
	penalty := 0.0
	if sig.DifferentLastName {
		penalty += 0.15
	}
	if sig.DifferentEmailDomain {
		penalty += 0.05
	}
	if sig.PhoneAsymmetry {
		penalty += 0.05
	}
	if sig.OrderNameConflict {
		penalty += 0.10
	}
	if sig.OrderAddressConflict {
		penalty += 0.12
	}

	result := base - penalty
	if result < 0 {
		return 0, ""
	}
	return result, "profile"
}

// computeBaseRules is the explicit rule table.
// Rules are ordered by evidence strength: Tier 1 (hard identity) →
// Tier 2 (strong two-signal corroboration) → Tier 3 (contextual) → Tier 4 (weak).
// Returning 0 means "no rule matched — try fallback."
func computeBaseRules(s Signals) float64 {
	// ── Tier 1: single hard-identity signal is sufficient ─────────────────────
	if s.EmailExact {
		return 0.98
	}
	if s.PhoneExact {
		return 0.98
	}

	// ── Tier 2: two strong corroborating signals ──────────────────────────────
	if s.AddressExact && s.NameHigh {
		return 0.92
	}
	if s.PhoneSuffix && s.NameHigh {
		return 0.90
	}
	if s.EmailLocalExact && s.NameHigh {
		return 0.88
	}
	if s.AddressExact && s.NameMedium {
		return 0.85
	}
	if s.PhoneSuffix && s.NameMedium {
		return 0.82
	}
	if s.EmailLocalExact && s.NameMedium {
		return 0.80
	}
	if s.EmailLocalFuzzy && s.NameHigh {
		return 0.80 // same local pattern, different provider — plausible same person
	}

	// ── Tier 3: name + one weaker contextual signal ───────────────────────────
	if s.NameHigh && s.AddressPartial {
		return 0.76
	}
	if s.EmailLocalFuzzy && s.NameMedium {
		return 0.74
	}
	if s.NameHigh && s.EmailDomainMatch {
		return 0.70
	}

	// ── Tier 4: below clustering threshold — logged but not auto-clustered ────
	if s.NameMedium && s.AddressExact {
		return 0.65
	}

	return 0.0
}

// computeFallback provides a soft score (capped at 0.64) for pairs where no
// discrete rule matched but there is partial evidence across multiple fields.
// Requires at least NameMedium — prevents name-less noise from scoring at all.
//
// Weight rationale (mirrors human intuition):
//   name 40% — the primary identifier
//   address 30% — strong physical anchor
//   email 20% — useful but spoofable / disposable
//   phone 10% — often missing or shared
func computeFallback(sig Signals, nameSim, addrSim, emailSim, phoneSim float64) float64 {
	if !sig.NameMedium && !sig.NameHigh {
		return 0
	}
	score := 0.4*nameSim + 0.3*addrSim + 0.2*emailSim + 0.1*phoneSim
	const cap = 0.64
	if score > cap {
		return cap
	}
	if score < 0 {
		return 0
	}
	return score
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// lastToken returns the last whitespace-separated token of a normalized name.
func lastToken(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
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
// string built from address1+city+zip+province+country so that map key
// ordering differences don't cause false mismatches.
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
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	a1 := norm(m["address1"])
	city := norm(m["city"])
	zip := norm(m["zip"])
	prov := norm(m["province"])
	country := norm(m["country"])
	if a1 == "" && city == "" && zip == "" {
		return ""
	}
	return a1 + "|" + city + "|" + zip + "|" + prov + "|" + country
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

// emailSimilarity returns a 0–1 UI display score for two normalized email addresses.
// Clustering uses the Signals struct (EmailLocalExact/Fuzzy) rather than this value.
//
// Cross-domain pairs: scored on local-part similarity only, capped much lower than
// same-domain pairs. Fixes the old bug where "john@gmail.com" vs "john@yahoo.com"
// scored 0.69 because "@gmail" and "@yahoo" share the ".com" suffix.
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
		return 0.8 * levenshteinSim(localA, localB)
	}
	localSim := levenshteinSim(localA, localB)
	switch {
	case localSim >= 0.99:
		return 0.35
	case localSim >= 0.92:
		return 0.25
	case localSim >= 0.75:
		return 0.15
	default:
		return 0.0
	}
}

// ── String distance functions ─────────────────────────────────────────────────

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

func jaroWinkler(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	jaro := jaroSim(a, b)
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

// ── Behavioral signal helpers ──────────────────────────────────────────────

func parseOrderAddresses(raw *json.RawMessage) []models.OrderAddress {
	if raw == nil || len(*raw) == 0 {
		return nil
	}
	var addrs []models.OrderAddress
	if err := json.Unmarshal(*raw, &addrs); err != nil {
		return nil
	}
	return addrs
}

func anyExactOrderAddressMatch(as, bs []models.OrderAddress) bool {
	for _, a := range as {
		for _, b := range bs {
			if strings.EqualFold(a.City, b.City) &&
				strings.EqualFold(a.Zip, b.Zip) &&
				strings.EqualFold(a.Country, b.Country) {
				return true
			}
		}
	}
	return false
}

func anyPartialOrderAddressMatch(as, bs []models.OrderAddress) bool {
	for _, a := range as {
		for _, b := range bs {
			addrA := strings.ToLower(strings.Join([]string{a.Street, a.City, a.Zip, a.Country}, " "))
			addrB := strings.ToLower(strings.Join([]string{b.Street, b.City, b.Zip, b.Country}, " "))
			if levenshteinSim(addrA, addrB) >= 0.85 {
				return true
			}
		}
	}
	return false
}

func anyHighOrderNameMatch(as, bs []string) bool {
	for _, a := range as {
		for _, b := range bs {
			if jaroWinkler(strings.ToLower(a), strings.ToLower(b)) >= 0.92 {
				return true
			}
		}
	}
	return false
}

// anyConflictingOrderNames returns true when at least one pair of order names
// from each side clearly differs (Jaro-Winkler ≤ 0.70). Indicates different people
// may have placed orders on these accounts.
func anyConflictingOrderNames(as, bs []string) bool {
	for _, a := range as {
		for _, b := range bs {
			if jaroWinkler(strings.ToLower(a), strings.ToLower(b)) <= 0.70 {
				return true
			}
		}
	}
	return false
}

// anyConflictingOrderCountries returns true when the two sides' order addresses
// span different countries — a strong signal of distinct people.
func anyConflictingOrderCountries(as, bs []models.OrderAddress) bool {
	countriesA := make(map[string]struct{})
	for _, a := range as {
		if a.Country != "" {
			countriesA[strings.ToUpper(a.Country)] = struct{}{}
		}
	}
	if len(countriesA) == 0 {
		return false
	}
	for _, b := range bs {
		if b.Country == "" {
			continue
		}
		if _, ok := countriesA[strings.ToUpper(b.Country)]; !ok {
			return true
		}
	}
	return false
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
