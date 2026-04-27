package identity

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/services/intelligence"
	"merger/backend/internal/utils"
)

const (
	// MinConfidence is the minimum combined score to log a pair at all.
	// With rule-based scoring, computeConfidence returns 0 or ≥ 0.65,
	// so this acts as a safety net against future regressions.
	MinConfidence = 0.30

	// DefaultThreshold is the cluster-formation threshold.
	// Raised from 0.45 to 0.65 to require two corroborating signals before
	// grouping. Name alone (0.0 from rule-based engine) never reaches this.
	DefaultThreshold = 0.65
)

type Detector struct {
	customerCacheRepo repository.CustomerCacheRepository
	duplicateRepo     repository.DuplicateRepository
	settingsRepo      repository.SettingsRepository
	analyzer          *intelligence.Analyzer // may be nil — analysis is skipped if so
	log               zerolog.Logger
}

func NewDetector(
	customerCacheRepo repository.CustomerCacheRepository,
	duplicateRepo repository.DuplicateRepository,
	settingsRepo repository.SettingsRepository,
	analyzer *intelligence.Analyzer,
	log zerolog.Logger,
) *Detector {
	return &Detector{
		customerCacheRepo: customerCacheRepo,
		duplicateRepo:     duplicateRepo,
		settingsRepo:      settingsRepo,
		analyzer:          analyzer,
		log:               log,
	}
}

// RunDetection loads all customers for the merchant, scores pairs, clusters them,
// and upserts duplicate_groups into the database.
func (d *Detector) RunDetection(ctx context.Context, merchantID uuid.UUID) error {
	customers, err := d.customerCacheRepo.FindByMerchant(ctx, merchantID)
	if err != nil {
		return fmt.Errorf("load customers: %w", err)
	}

	if len(customers) < 2 {
		d.log.Debug().Str("merchant", merchantID.String()).Msg("too few customers to detect duplicates")
		return nil
	}

	d.log.Info().Int("count", len(customers)).Str("merchant", merchantID.String()).Msg("running detection")

	// Load merchant signal settings. Defaults are the safe/conservative direction
	// (all signals enabled, all risk guards active) so a DB miss never loosens scoring.
	scoreOpts := ScoreOptions{
		BehavioralEnabled: false,
		SignalEmail:       true,
		SignalPhone:       true,
		SignalAddress:     true,
		SignalName:        true,
	}
	weakLinkProtection   := true
	requireAnchor        := true
	blockDisabledAccounts := true
	blockFraudTags        := true
	blockDifferentCountry := true
	if d.settingsRepo != nil {
		if settings, err := d.settingsRepo.Get(ctx, merchantID); err == nil {
			scoreOpts.BehavioralEnabled = settings.EnableBehavioralSignals
			scoreOpts.SignalEmail = settings.SignalEmail
			scoreOpts.SignalPhone = settings.SignalPhone
			scoreOpts.SignalAddress = settings.SignalAddress
			scoreOpts.SignalName = settings.SignalName
			weakLinkProtection = settings.WeakLinkProtection
			requireAnchor = settings.RequireAnchor
			blockDisabledAccounts = settings.BlockDisabledAccounts
			blockFraudTags = settings.BlockFraudTags
			blockDifferentCountry = settings.BlockDifferentCountry
		}
	}
	d.log.Info().
		Bool("behavioral_enabled", scoreOpts.BehavioralEnabled).
		Bool("signal_email", scoreOpts.SignalEmail).
		Bool("signal_phone", scoreOpts.SignalPhone).
		Bool("signal_address", scoreOpts.SignalAddress).
		Bool("signal_name", scoreOpts.SignalName).
		Bool("weak_link_protection", weakLinkProtection).
		Bool("require_anchor", requireAnchor).
		Bool("block_disabled_accounts", blockDisabledAccounts).
		Bool("block_fraud_tags", blockFraudTags).
		Bool("block_different_country", blockDifferentCountry).
		Msg("detection: signal settings")

	// Clear stale pending groups before rebuilding — ensures merged/deleted
	// customers don't produce ghost groups on re-scan.
	if deleted, err := d.duplicateRepo.DeletePendingByMerchant(ctx, merchantID); err != nil {
		d.log.Warn().Err(err).Msg("detection: failed to clear pending groups")
	} else if deleted > 0 {
		d.log.Info().Int64("deleted", deleted).Msg("detection: cleared stale pending groups")
	}

	// Build an index so we can look up full CustomerCache rows by Shopify ID.
	customerByID := make(map[int64]*models.CustomerCache, len(customers))
	for i := range customers {
		customerByID[customers[i].ShopifyCustomerID] = &customers[i]
	}

	// Normalize all customers in-place
	for i := range customers {
		customers[i].Email = utils.NormalizeEmail(customers[i].Email)
		customers[i].Name = utils.NormalizeName(customers[i].Name)
		customers[i].Phone = utils.NormalizePhone(customers[i].Phone)
	}

	pairs := d.scorePairs(customers, scoreOpts)
	clusters := ClusterPairs(pairs, DefaultThreshold)

	persisted := 0
	for _, memberIDs := range clusters {
		hash := groupHash(memberIDs)
		topPair := topPairForCluster(pairs, memberIDs)
		maxScore := 0.0
		if topPair != nil {
			maxScore = topPair.Score
		}

		weakestEdge := WeakestClusterEdge(pairs, memberIDs)
		members := gatherMembers(memberIDs, customerByID)

		// Detect conflicts unconditionally — needed for both risk classification
		// and the intelligence report. Per-customer checks (disabled_account,
		// different_countries, risk_tag) are aggregate by nature so one call
		// covers all cluster members.
		cr := intelligence.DetectConflicts(members)
		conflictSeverity := cr.Severity

		// Generate intelligence report and enrich with breakdown, reasons,
		// and structural conflict analysis.
		var (
			intelJSON      []byte
			readinessScore *float64
		)
		if d.analyzer != nil {
			if report, err := d.analyzer.Analyze(members); err == nil {
				// Per-field breakdown with human-readable reasons.
				if topPair != nil {
					reasons := intelligence.GenerateBreakdownReasons(
						topPair.EmailSim, topPair.NameSim,
						topPair.PhoneSim, topPair.AddressSim,
					)
					report.Breakdown = &intelligence.FieldBreakdown{
						EmailScore:   topPair.EmailSim,
						NameScore:    topPair.NameSim,
						PhoneScore:   topPair.PhoneSim,
						AddressScore: topPair.AddressSim,
						Reasons:      reasons,
					}
				}

				// Populate behavioral signals and confidence source from the top pair.
				if topPair != nil {
					report.BehavioralSignals = &intelligence.BehavioralSignals{
						OrderAddressExact:    topPair.Sig.OrderAddressExact,
						OrderAddressPartial:  topPair.Sig.OrderAddressPartial,
						OrderNameHigh:        topPair.Sig.OrderNameHigh,
						RecentOrderOverlap:   topPair.Sig.RecentOrderOverlap,
						OrderNameConflict:    topPair.Sig.OrderNameConflict,
						OrderAddressConflict: topPair.Sig.OrderAddressConflict,
						NoteShared:           topPair.Sig.NoteShared,
					}
					report.ConfidenceSource = topPair.ConfidenceSource
				}

				// Attach conflict data (already computed above).
				report.Conflicts = cr.Conflicts
				report.ConflictSeverity = cr.Severity

				// One-line confidence summary for the UI.
				var breakdownReasons []intelligence.ReasonItem
				if report.Breakdown != nil {
					breakdownReasons = report.Breakdown.Reasons
				}
				report.Summary = intelligence.GenerateSummary(breakdownReasons, cr.Conflicts, maxScore)

				if raw, err2 := report.ToRawJSON(); err2 == nil {
					intelJSON = raw
					readinessScore = &report.ReadinessScore
				}
			} else {
				d.log.Debug().Err(err).Str("hash", hash).Msg("intelligence analysis skipped")
			}
		}

		density := ClusterDensity(pairs, memberIDs)
		hasAnchor := clusterHasAnchor(members)
		br := ComputeBusinessRisk(members)

		// Pairwise conflict spread: iterate every member pair explicitly.
		// intelligence.DetectConflicts(members) already covers this via field
		// aggregation, but the pairwise pass guarantees correctness for any
		// future conflict type that requires comparing two specific customers
		// (e.g. A has US, C has UK, B has no country — aggregate catches it,
		// but pairwise makes the guarantee explicit and easier to audit).
		pairwiseSev := pairwiseConflictSeverity(members)
		if sevRank(pairwiseSev) > sevRank(conflictSeverity) {
			conflictSeverity = pairwiseSev
		}

		// Compute adjusted severity for risk classification: filter out conflict
		// types the merchant has opted out of as hard blockers. The raw
		// conflictSeverity is still stored in the intelligence report for audit.
		classifySeverity := adjustedConflictSeverity(
			cr.Conflicts,
			blockDisabledAccounts, blockFraudTags, blockDifferentCountry,
		)

		riskLevel := classifyRisk(riskInput{
			confidence:         maxScore,
			conflictSeverity:   classifySeverity,
			weakestEdge:        weakestEdge,
			density:            density,
			hasAnchor:          hasAnchor,
			businessRisk:       br.Level,
			impactScore:        br.ImpactScore,
			weakLinkProtection: weakLinkProtection,
			requireAnchor:      requireAnchor,
		})

		var businessRiskLevel *string
		if br.Level != "" {
			businessRiskLevel = &br.Level
		}
		g := &models.DuplicateGroup{
			MerchantID:        merchantID,
			GroupHash:         hash,
			CustomerIDs:       int64SliceToPQ(memberIDs),
			ConfidenceScore:   maxScore,
			Status:            "pending",
			RiskLevel:         &riskLevel,
			ReadinessScore:    readinessScore,
			IntelligenceJSON:  intelJSON,
			BusinessRiskLevel: businessRiskLevel,
			ImpactScore:       &br.ImpactScore,
		}

		if err := d.duplicateRepo.CreateGroup(ctx, g); err != nil {
			d.log.Warn().Err(err).Str("hash", hash).Msg("upsert duplicate group")
			continue
		}
		persisted++
	}

	d.log.Info().Int("groups", persisted).Str("merchant", merchantID.String()).Msg("detection complete")
	return nil
}

// scorePairs generates scored pairs using three bucket strategies:
//  1. Email-domain bucketing (primary)
//  2. Phone suffix bucketing (last 7 digits of normalized phone)
//  3. Address hash bucketing (city+zip combination)
//
// A seen map deduplicates pairs found by multiple buckets.
func (d *Detector) scorePairs(customers []models.CustomerCache, opts ScoreOptions) []ScoredPair {
	seen := make(map[[2]int64]struct{})
	var pairs []ScoredPair

	addPair := func(a, b *models.CustomerCache) {
		lo, hi := a.ShopifyCustomerID, b.ShopifyCustomerID
		if lo > hi {
			lo, hi = hi, lo
		}
		key := [2]int64{lo, hi}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		s := ScorePair(a, b, opts)
		d.log.Debug().
			Int64("a", a.ShopifyCustomerID).
			Int64("b", b.ShopifyCustomerID).
			Str("aName", a.Name).
			Str("bName", b.Name).
			Float64("email", s.EmailSim).
			Float64("name", s.NameSim).
			Float64("phone", s.PhoneSim).
			Float64("addr", s.AddressSim).
			Float64("combined", s.Combined).
			Bool("sig.emailExact", s.Sig.EmailExact).
			Bool("sig.emailLocalExact", s.Sig.EmailLocalExact).
			Bool("sig.emailLocalFuzzy", s.Sig.EmailLocalFuzzy).
			Bool("sig.emailDomainMatch", s.Sig.EmailDomainMatch).
			Bool("sig.phoneExact", s.Sig.PhoneExact).
			Bool("sig.phoneSuffix", s.Sig.PhoneSuffix).
			Bool("sig.nameHigh", s.Sig.NameHigh).
			Bool("sig.nameMedium", s.Sig.NameMedium).
			Bool("sig.addressExact", s.Sig.AddressExact).
			Bool("sig.addressPartial", s.Sig.AddressPartial).
			Bool("sig.orderAddrExact", s.Sig.OrderAddressExact).
			Bool("sig.orderAddrPartial", s.Sig.OrderAddressPartial).
			Bool("sig.orderNameHigh", s.Sig.OrderNameHigh).
			Bool("sig.recentOrderOverlap", s.Sig.RecentOrderOverlap).
			Bool("sig.diffLastName", s.Sig.DifferentLastName).
			Bool("sig.diffEmailDomain", s.Sig.DifferentEmailDomain).
			Bool("sig.phoneAsymmetry", s.Sig.PhoneAsymmetry).
			Bool("sig.orderNameConflict", s.Sig.OrderNameConflict).
			Bool("sig.orderAddrConflict", s.Sig.OrderAddressConflict).
			Str("confidenceSource", s.ConfidenceSource).
			Msg("bucket pair score")
		if s.Combined >= MinConfidence {
			pairs = append(pairs, ScoredPair{
				A:                a.ShopifyCustomerID,
				B:                b.ShopifyCustomerID,
				Score:            s.Combined,
				EmailSim:         s.EmailSim,
				NameSim:          s.NameSim,
				PhoneSim:         s.PhoneSim,
				AddressSim:       s.AddressSim,
				ConfidenceSource: s.ConfidenceSource,
				Sig:              s.Sig,
			})
		}
	}

	scoreBucket := func(buckets map[string][]int) {
		for _, indices := range buckets {
			for i := 0; i < len(indices); i++ {
				for j := i + 1; j < len(indices); j++ {
					addPair(&customers[indices[i]], &customers[indices[j]])
				}
			}
		}
	}

	// Bucket 1: email domain
	domainBuckets := make(map[string][]int)
	for i, c := range customers {
		domain := utils.EmailDomain(c.Email)
		if domain == "" {
			domain = "__no_domain__"
		}
		domainBuckets[domain] = append(domainBuckets[domain], i)
	}
	scoreBucket(domainBuckets)

	// Bucket 2: phone suffix (last 7 digits) — catches cross-email phone matches
	phoneBuckets := make(map[string][]int)
	for i, c := range customers {
		phone := utils.NormalizePhone(c.Phone)
		if len(phone) >= 7 {
			suffix := phone[len(phone)-7:]
			phoneBuckets[suffix] = append(phoneBuckets[suffix], i)
		}
	}
	scoreBucket(phoneBuckets)

	// Bucket 3: address hash (city+zip) — catches shared-address households
	addrBuckets := make(map[string][]int)
	for i, c := range customers {
		key := addressBucketKey(&c)
		if key != "" {
			addrBuckets[key] = append(addrBuckets[key], i)
		}
	}
	scoreBucket(addrBuckets)

	// Cross-domain pass: high-confidence name match for customers with different
	// email domains (e.g. john@gmail.com / john@company.com).
	// Uses the shared seen map so pairs found by buckets above are not rescored.
	// Limit to 500 customers to avoid O(n²) at scale.
	if len(customers) <= 500 {
		for i := 0; i < len(customers); i++ {
			for j := i + 1; j < len(customers); j++ {
				a := &customers[i]
				b := &customers[j]
				if utils.EmailDomain(a.Email) == utils.EmailDomain(b.Email) {
					continue // already covered by domain bucket
				}
				nameSim := jaroWinkler(a.Name, b.Name)
				d.log.Debug().
					Int64("a", a.ShopifyCustomerID).
					Int64("b", b.ShopifyCustomerID).
					Str("aName", a.Name).
					Str("bName", b.Name).
					Float64("nameSim", nameSim).
					Msg("cross-domain name check")
				if nameSim >= 0.82 {
					addPair(a, b) // addPair deduplicates internally
				}
			}
		}
	}

	return pairs
}

// addressBucketKey builds a normalized key for address-based bucketing.
//
// Strategy (most precise to least):
//   - If a street address is available: normalizedStreet|city|zip
//   - Otherwise: city|zip (broader bucket, more collisions but still useful)
//
// The street is normalized by stripping unit/suite/apt numbers so that
// "123 Main St Apt 2" and "123 Main St Unit 4" end up in the same bucket.
func addressBucketKey(c *models.CustomerCache) string {
	if len(c.AddressJSON) == 0 {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal(c.AddressJSON, &m); err != nil {
		return ""
	}
	city := strings.ToLower(strings.TrimSpace(m["city"]))
	zip := strings.ToLower(strings.TrimSpace(m["zip"]))
	if city == "" && zip == "" {
		return ""
	}

	street := normalizeStreet(m["address1"])
	if street != "" {
		return street + "|" + city + "|" + zip
	}
	return city + "|" + zip
}

// normalizeStreet lowercases and strips common unit/suite suffixes so that
// "123 Main St Apt 2B" and "123 Main St Suite 100" resolve to "123 main st".
func normalizeStreet(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// Strip everything from common unit delimiters onwards.
	unitPrefixes := []string{" apt ", " apt.", " unit ", " suite ", " ste ", " #", " no."}
	for _, prefix := range unitPrefixes {
		if idx := strings.Index(s, prefix); idx > 0 {
			s = strings.TrimSpace(s[:idx])
		}
	}
	return s
}

// gatherMembers collects full CustomerCache rows for a list of Shopify IDs.
func gatherMembers(ids []int64, index map[int64]*models.CustomerCache) []models.CustomerCache {
	out := make([]models.CustomerCache, 0, len(ids))
	for _, id := range ids {
		if c := index[id]; c != nil {
			out = append(out, *c)
		}
	}
	return out
}

func groupHash(ids []int64) string {
	sorted := make([]int64, len(ids))
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	h := sha256.New()
	for _, id := range sorted {
		fmt.Fprintf(h, "%d,", id)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// topPairForCluster returns the highest-scoring pair whose both members belong
// to the given cluster. Returns nil if no pair is found.
func topPairForCluster(pairs []ScoredPair, memberIDs []int64) *ScoredPair {
	memberSet := make(map[int64]bool, len(memberIDs))
	for _, id := range memberIDs {
		memberSet[id] = true
	}
	var top *ScoredPair
	for i := range pairs {
		p := &pairs[i]
		if memberSet[p.A] && memberSet[p.B] {
			if top == nil || p.Score > top.Score {
				top = p
			}
		}
	}
	return top
}

// riskInput bundles all factors used to classify a cluster's risk level.
// Using a struct keeps classifyRisk's signature stable as new factors are added.
type riskInput struct {
	confidence          float64
	conflictSeverity    string  // from intelligence.DetectConflicts + pairwise spread
	weakestEdge         float64
	density             float64
	hasAnchor           bool
	businessRisk        string  // from ComputeBusinessRisk: "high"|"medium"|"low"|""
	impactScore         float64 // blast-radius: cluster_size × avg_customer_value
	weakLinkProtection  bool    // when false, step 4 (weak interior edge) is skipped
	requireAnchor       bool    // when false, step 6 (no identity anchor) is skipped
}

// classifyRisk maps cluster evidence to a risk level string.
//
// Priority chain (highest override first):
//  1. Structural conflicts — different countries, disabled accounts, risk tags
//  2. Business risk        — high-value accounts with stark history disparity
//  3. Blast radius         — combined value ≥ $1,000 forces manual review
//  4. Weak interior edge   — borderline link may span unrelated people
//  5. Sparse cluster       — transitive bridge topology, not direct corroboration
//  6. No identity anchor   — all ghost-like or newly-created records
//  7. Confidence thresholds — clean cluster, pure signal quality
func classifyRisk(in riskInput) string {
	// 1. Structural conflicts override everything.
	switch in.conflictSeverity {
	case "high":
		return "risky"
	case "medium":
		if in.confidence >= 0.90 {
			return "review"
		}
		return "risky"
	}

	// 2. Business risk: high commercial stakes warrant a human eye.
	if in.businessRisk == "high" {
		if in.confidence >= 0.90 {
			return "review"
		}
		return "risky"
	}
	if in.businessRisk == "medium" && in.confidence >= 0.90 {
		return "review" // cap "safe" at "review" for medium business risk
	}

	// 3. Blast-radius guardrail: never auto-merge when the combined value at
	// stake exceeds the threshold, regardless of how clean identity signals look.
	const highImpactFloor = 1000.0
	if in.impactScore >= highImpactFloor && in.confidence >= 0.90 {
		return "review"
	}

	// 4. Weak interior edge — cluster may include an unrelated person.
	// Skipped when the merchant has disabled weak_link_protection (they accept
	// the risk of loosely-connected clusters being auto-merged as safe).
	const weakEdgeFloor = 0.70
	if in.weakLinkProtection && in.weakestEdge < weakEdgeFloor {
		if in.confidence >= 0.75 {
			return "review"
		}
		return "risky"
	}

	// 5. Sparse cluster — evidence routes through a hub rather than direct links.
	const minDensity = 0.60
	if in.density < minDensity {
		if in.confidence >= 0.90 && in.weakestEdge >= 0.85 {
			return "review"
		}
		if in.confidence >= 0.75 {
			return "review"
		}
		return "risky"
	}

	// 6. No strong anchor — cluster consists entirely of ghost-like records.
	// Skipped when the merchant has disabled require_anchor (they are willing
	// to auto-merge clusters that lack a verified identity anchor).
	if in.requireAnchor && !in.hasAnchor {
		if in.confidence >= 0.90 {
			return "review"
		}
		return "risky"
	}

	// 7. Clean cluster — use confidence thresholds.
	switch {
	case in.confidence >= 0.90:
		return "safe"
	case in.confidence >= 0.75:
		return "review"
	default:
		return "risky"
	}
}

// pairwiseConflictSeverity returns the highest conflict severity found across
// all pairwise combinations of cluster members. This makes the "conflict spread"
// guarantee explicit: if A vs C has a high-severity conflict, the cluster is
// risky even if neither A→B nor B→C showed any conflict individually.
//
// For clusters of 2, this is equivalent to calling DetectConflicts directly.
// For larger clusters, short-circuits on the first "high" result.
func pairwiseConflictSeverity(members []models.CustomerCache) string {
	if len(members) <= 2 {
		return intelligence.DetectConflicts(members).Severity
	}
	maxSev := ""
	for i := 0; i < len(members); i++ {
		for j := i + 1; j < len(members); j++ {
			pair := []models.CustomerCache{members[i], members[j]}
			s := intelligence.DetectConflicts(pair).Severity
			if sevRank(s) > sevRank(maxSev) {
				maxSev = s
				if maxSev == "high" {
					return "high" // short-circuit — can't get worse
				}
			}
		}
	}
	return maxSev
}

// sevRank maps a severity string to an integer for comparison.
func sevRank(s string) int {
	switch s {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func int64SliceToPQ(ids []int64) []int64 {
	result := make([]int64, len(ids))
	copy(result, ids)
	return result
}

// adjustedConflictSeverity recomputes the highest conflict severity after
// filtering out conflict types that the merchant has opted out of as hard
// blockers. The raw conflicts are still stored in the intelligence report.
//
// This prevents a disabled_account or fraud-tag conflict from forcing
// risk_level = "risky" when the merchant has explicitly turned off that guard.
func adjustedConflictSeverity(conflicts []intelligence.ConflictItem, blockDisabledAccounts, blockFraudTags, blockDifferentCountry bool) string {
	max := 0
	for _, c := range conflicts {
		if c.Type == "disabled_account" && !blockDisabledAccounts {
			continue
		}
		if strings.HasPrefix(c.Type, "risk_tag:") && !blockFraudTags {
			continue
		}
		if c.Type == "different_countries" && !blockDifferentCountry {
			continue
		}
		if sev := sevRank(c.Severity); sev > max {
			max = sev
		}
	}
	switch max {
	case 3:
		return "high"
	case 2:
		return "medium"
	case 1:
		return "low"
	default:
		return ""
	}
}
