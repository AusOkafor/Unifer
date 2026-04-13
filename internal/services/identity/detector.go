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
	// MinConfidence is the minimum score to consider a pair potentially duplicate.
	MinConfidence = 0.25
	// DefaultThreshold is the default cluster-formation threshold.
	// Lowered to 0.45 so name+address matches (no email) are captured.
	DefaultThreshold = 0.45
)

type Detector struct {
	customerCacheRepo repository.CustomerCacheRepository
	duplicateRepo     repository.DuplicateRepository
	analyzer          *intelligence.Analyzer // may be nil — analysis is skipped if so
	log               zerolog.Logger
}

func NewDetector(
	customerCacheRepo repository.CustomerCacheRepository,
	duplicateRepo repository.DuplicateRepository,
	analyzer *intelligence.Analyzer,
	log zerolog.Logger,
) *Detector {
	return &Detector{
		customerCacheRepo: customerCacheRepo,
		duplicateRepo:     duplicateRepo,
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

	pairs := d.scorePairs(customers)
	clusters := ClusterPairs(pairs, DefaultThreshold)

	persisted := 0
	for _, memberIDs := range clusters {
		hash := groupHash(memberIDs)
		topPair := topPairForCluster(pairs, memberIDs)
		maxScore := 0.0
		if topPair != nil {
			maxScore = topPair.Score
		}

		riskLevel := classifyRisk(maxScore)
		g := &models.DuplicateGroup{
			MerchantID:      merchantID,
			GroupHash:       hash,
			CustomerIDs:     int64SliceToPQ(memberIDs),
			ConfidenceScore: maxScore,
			Status:          "pending",
			RiskLevel:       &riskLevel,
		}

		// Generate intelligence report from cached customer data.
		if d.analyzer != nil {
			members := gatherMembers(memberIDs, customerByID)
			if report, err := d.analyzer.Analyze(members); err == nil {
				// Attach per-field breakdown from the top-scoring pair.
				if topPair != nil {
					report.Breakdown = &intelligence.FieldBreakdown{
						EmailScore:   topPair.EmailSim,
						NameScore:    topPair.NameSim,
						PhoneScore:   topPair.PhoneSim,
						AddressScore: topPair.AddressSim,
					}
				}
				if raw, err := report.ToRawJSON(); err == nil {
					g.IntelligenceJSON = raw
					g.ReadinessScore = &report.ReadinessScore
				}
			} else {
				d.log.Debug().Err(err).Str("hash", hash).Msg("intelligence analysis skipped")
			}
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
func (d *Detector) scorePairs(customers []models.CustomerCache) []ScoredPair {
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
		s := ScorePair(a, b)
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
			Msg("bucket pair score")
		if s.Combined >= MinConfidence {
			pairs = append(pairs, ScoredPair{
				A:          a.ShopifyCustomerID,
				B:          b.ShopifyCustomerID,
				Score:      s.Combined,
				EmailSim:   s.EmailSim,
				NameSim:    s.NameSim,
				PhoneSim:   s.PhoneSim,
				AddressSim: s.AddressSim,
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

// addressBucketKey builds a normalized city+zip key used to bucket customers
// who may share a household. Returns "" if neither city nor zip is available.
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
	return city + "|" + zip
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

// classifyRisk maps a confidence score to a risk level string.
func classifyRisk(confidence float64) string {
	switch {
	case confidence >= 0.90:
		return "safe"
	case confidence >= 0.75:
		return "review"
	default:
		return "risky"
	}
}

func int64SliceToPQ(ids []int64) []int64 {
	result := make([]int64, len(ids))
	copy(result, ids)
	return result
}
