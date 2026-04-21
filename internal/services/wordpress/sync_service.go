package wordpress

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/repository"
)

// SyncService ingests WordPress users into the customer cache.
type SyncService struct {
	customerCacheRepo repository.CustomerCacheRepository
	log               zerolog.Logger
}

func NewSyncService(customerCacheRepo repository.CustomerCacheRepository, log zerolog.Logger) *SyncService {
	return &SyncService{customerCacheRepo: customerCacheRepo, log: log}
}

// IngestUsers upserts a batch of WordPress users into customer_cache with
// platform="wordpress". V1 does not call DeleteStaleEntries because WP push
// is not guaranteed to be a full snapshot.
func (s *SyncService) IngestUsers(ctx context.Context, merchantID uuid.UUID, users []WPUser) (int, error) {
	ingested := 0
	for _, u := range users {
		c := MapWPUserToCustomerCache(merchantID, u)
		if err := s.customerCacheRepo.Upsert(ctx, c); err != nil {
			return ingested, fmt.Errorf("wp sync: upsert user %d: %w", u.ID, err)
		}
		ingested++
	}
	s.log.Info().
		Str("merchant_id", merchantID.String()).
		Int("ingested", ingested).
		Msg("wp sync: users ingested")
	return ingested, nil
}
