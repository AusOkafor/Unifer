package wordpress

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/repository"
)

// SyncService ingests WooCommerce customers (registered + guest) into the customer cache.
type SyncService struct {
	customerCacheRepo repository.CustomerCacheRepository
	log               zerolog.Logger
}

func NewSyncService(customerCacheRepo repository.CustomerCacheRepository, log zerolog.Logger) *SyncService {
	return &SyncService{customerCacheRepo: customerCacheRepo, log: log}
}

// IngestCustomers upserts a batch of WooCommerce customers into customer_cache with
// platform="wordpress". Includes both registered users and guest order-only customers.
// V1 does not call DeleteStaleEntries because WP push is not guaranteed to be a full snapshot.
func (s *SyncService) IngestCustomers(ctx context.Context, merchantID uuid.UUID, customers []WCCustomer) (int, error) {
	ingested := 0
	for _, c := range customers {
		row := MapWCCustomerToCustomerCache(merchantID, c)
		if err := s.customerCacheRepo.Upsert(ctx, row); err != nil {
			return ingested, fmt.Errorf("wp sync: upsert customer (email=%s): %w", c.Email, err)
		}
		ingested++
	}
	s.log.Info().
		Str("merchant_id", merchantID.String()).
		Int("ingested", ingested).
		Msg("wc sync: customers ingested")
	return ingested, nil
}
