package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	shopifysvc "merger/backend/internal/services/shopify"
	"merger/backend/internal/utils"
)

// Service syncs all customers from Shopify into the local customer_cache.
type Service struct {
	merchantRepo      repository.MerchantRepository
	customerCacheRepo repository.CustomerCacheRepository
	encryptor         *utils.Encryptor
	log               zerolog.Logger
}

func NewService(
	merchantRepo repository.MerchantRepository,
	customerCacheRepo repository.CustomerCacheRepository,
	encryptor *utils.Encryptor,
	log zerolog.Logger,
) *Service {
	return &Service{
		merchantRepo:      merchantRepo,
		customerCacheRepo: customerCacheRepo,
		encryptor:         encryptor,
		log:               log,
	}
}

// SyncCustomers fetches all customers from the merchant's Shopify store and
// upserts them into customer_cache. Returns the number of customers synced.
func (s *Service) SyncCustomers(ctx context.Context, merchantID uuid.UUID) (int, error) {
	merchant, err := s.merchantRepo.FindByID(ctx, merchantID)
	if err != nil {
		return 0, fmt.Errorf("find merchant: %w", err)
	}

	token, err := s.encryptor.Decrypt(merchant.AccessTokenEnc)
	if err != nil {
		return 0, fmt.Errorf("decrypt token: %w", err)
	}

	client := shopifysvc.NewClient(merchant.ShopDomain, token, s.log)
	customerSvc := shopifysvc.NewCustomerService(client)

	customers, err := customerSvc.FetchAll(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch all customers: %w", err)
	}

	s.log.Info().Int("count", len(customers)).Str("shop", merchant.ShopDomain).Msg("sync: fetched customers")

	activeIDs := make([]int64, 0, len(customers))
	for _, sc := range customers {
		addrJSON := buildAddressJSON(sc)
		tags := parseTags(sc.Tags)

		cache := &models.CustomerCache{
			MerchantID:        merchantID,
			ShopifyCustomerID: sc.ID,
			Name:              strings.TrimSpace(sc.FirstName + " " + sc.LastName),
			Email:             sc.Email,
			Phone:             sc.Phone,
			Tags:              tags,
			OrdersCount:       sc.OrdersCount,
			TotalSpent:        sc.TotalSpent,
			AddressJSON:       addrJSON,
			UpdatedAt:         time.Now(),
		}

		if err := s.customerCacheRepo.Upsert(ctx, cache); err != nil {
			s.log.Warn().Err(err).Int64("shopify_id", sc.ID).Msg("sync: upsert failed, skipping")
		} else {
			activeIDs = append(activeIDs, sc.ID)
		}
	}

	// Remove cached customers that no longer exist in Shopify (merged or deleted).
	removed, err := s.customerCacheRepo.DeleteStaleEntries(ctx, merchantID, activeIDs)
	if err != nil {
		s.log.Error().Err(err).Msg("sync: stale entry cleanup failed")
	} else {
		s.log.Info().Int64("removed", removed).Int("active", len(activeIDs)).Msg("sync: stale entry cleanup complete")
	}

	return len(customers), nil
}

func buildAddressJSON(sc shopifysvc.ShopifyCustomer) json.RawMessage {
	if len(sc.Addresses) == 0 {
		return json.RawMessage("{}")
	}
	addr := sc.Addresses[0]
	m := map[string]string{
		"address1": addr.Address1,
		"city":     addr.City,
		"province": addr.Province,
		"zip":      addr.Zip,
		"country":  addr.Country,
	}
	b, _ := json.Marshal(m)
	return b
}

func parseTags(raw string) []string {
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
