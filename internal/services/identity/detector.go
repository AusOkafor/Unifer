package identity

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"

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
		maxScore := maxClusterScore(pairs, memberIDs)

		g := &models.DuplicateGroup{
			MerchantID:      merchantID,
			GroupHash:       hash,
			CustomerIDs:     int64SliceToPQ(memberIDs),
			ConfidenceScore: maxScore,
			Status:          "pending",
		}

		// Generate intelligence report from cached customer data.
		if d.analyzer != nil {
			members := gatherMembers(memberIDs, customerByID)
			if report, err := d.analyzer.Analyze(members); err == nil {
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

// scorePairs generates scored pairs using email-domain bucketing + cross-domain name pass.
func (d *Detector) scorePairs(customers []models.CustomerCache) []ScoredPair {
	// Bucket by email domain
	domainBuckets := make(map[string][]int)
	for i, c := range customers {
		domain := utils.EmailDomain(c.Email)
		if domain == "" {
			domain = "__no_domain__"
		}
		domainBuckets[domain] = append(domainBuckets[domain], i)
	}

	var pairs []ScoredPair

	// Score within each domain bucket
	for _, indices := range domainBuckets {
		for i := 0; i < len(indices); i++ {
			for j := i + 1; j < len(indices); j++ {
				a := &customers[indices[i]]
				b := &customers[indices[j]]
				s := ScorePair(a, b)
				if s.Combined >= MinConfidence {
					pairs = append(pairs, ScoredPair{
						A:     a.ShopifyCustomerID,
						B:     b.ShopifyCustomerID,
						Score: s.Combined,
					})
				}
			}
		}
	}

	// Cross-domain pass: compare by name only for customers with different domains
	// (catches name matches like john@gmail.com / john@company.com)
	// Limit to 500 customers to avoid explosive cross-product
	if len(customers) <= 500 {
		seen := make(map[[2]int64]bool)
		for i := 0; i < len(customers); i++ {
			for j := i + 1; j < len(customers); j++ {
				a := &customers[i]
				b := &customers[j]
				if utils.EmailDomain(a.Email) == utils.EmailDomain(b.Email) {
					continue // already covered above
				}
				key := [2]int64{a.ShopifyCustomerID, b.ShopifyCustomerID}
				if seen[key] {
					continue
				}
				seen[key] = true
				nameSim := jaroWinkler(a.Name, b.Name)
				if nameSim >= 0.82 { // high name similarity → score the full pair
					s := ScorePair(a, b)
					if s.Combined >= MinConfidence {
						pairs = append(pairs, ScoredPair{
							A:     a.ShopifyCustomerID,
							B:     b.ShopifyCustomerID,
							Score: s.Combined,
						})
					}
				}
			}
		}
	}

	return pairs
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

func maxClusterScore(pairs []ScoredPair, memberIDs []int64) float64 {
	memberSet := make(map[int64]bool, len(memberIDs))
	for _, id := range memberIDs {
		memberSet[id] = true
	}
	max := 0.0
	for _, p := range pairs {
		if memberSet[p.A] && memberSet[p.B] && p.Score > max {
			max = p.Score
		}
	}
	return max
}

func int64SliceToPQ(ids []int64) []int64 {
	result := make([]int64, len(ids))
	copy(result, ids)
	return result
}
