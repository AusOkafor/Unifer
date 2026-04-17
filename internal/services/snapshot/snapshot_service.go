package snapshot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	shopifysvc "merger/backend/internal/services/shopify"
)

// ErrSnapshotNotFound is returned when no row exists for the snapshot ID.
var ErrSnapshotNotFound = errors.New("snapshot_not_found")

// SnapshotData is the full pre-merge state stored in the snapshot.
type SnapshotData struct {
	Customers []shopifysvc.ShopifyCustomer        `json:"customers"`
	Orders    map[int64][]shopifysvc.ShopifyOrder  `json:"orders"` // keyed by shopify customer ID
}

type Service struct {
	snapshotRepo repository.SnapshotRepository
}

func NewService(snapshotRepo repository.SnapshotRepository) *Service {
	return &Service{snapshotRepo: snapshotRepo}
}

// CreateFromCache builds a snapshot from customer_cache rows (no REST API calls needed).
// Orders are omitted in this path — snapshot captures identity data for reconstruction.
func (s *Service) CreateFromCache(ctx context.Context, merchantID uuid.UUID, customers []models.CustomerCache) (*models.Snapshot, error) {
	data := SnapshotData{
		Orders: make(map[int64][]shopifysvc.ShopifyOrder),
	}

	for _, c := range customers {
		sc := cacheToShopifyCustomer(c)
		data.Customers = append(data.Customers, sc)
		// Orders not captured here (REST protected data restriction).
		// The Shopify customerMerge API consolidates orders automatically.
	}

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("snapshot: marshal data: %w", err)
	}

	snap := &models.Snapshot{
		MerchantID: merchantID,
		Data:       dataJSON,
	}
	if err := s.snapshotRepo.Create(ctx, snap); err != nil {
		return nil, fmt.Errorf("snapshot: persist: %w", err)
	}

	return snap, nil
}

// cacheToShopifyCustomer converts a CustomerCache row into the ShopifyCustomer
// shape used for snapshot storage.
func cacheToShopifyCustomer(c models.CustomerCache) shopifysvc.ShopifyCustomer {
	sc := shopifysvc.ShopifyCustomer{
		ID:          c.ShopifyCustomerID,
		Email:       c.Email,
		Phone:       c.Phone,
		Tags:        strings.Join(c.Tags, ","),
		OrdersCount: c.OrdersCount,
		TotalSpent:  c.TotalSpent,
	}

	// Split name into first/last.
	parts := strings.SplitN(strings.TrimSpace(c.Name), " ", 2)
	if len(parts) >= 1 {
		sc.FirstName = parts[0]
	}
	if len(parts) >= 2 {
		sc.LastName = parts[1]
	}

	// Parse address JSON into a single Address entry if present.
	if len(c.AddressJSON) > 0 {
		raw := strings.TrimSpace(string(c.AddressJSON))
		if raw != "{}" && raw != "null" && raw != "" {
			var m map[string]string
			if err := json.Unmarshal(c.AddressJSON, &m); err == nil {
				sc.Addresses = []shopifysvc.Address{{
					Address1: m["address1"],
					City:     m["city"],
					Province: m["province"],
					Zip:      m["zip"],
					Country:  m["country"],
				}}
			}
		}
	}

	return sc
}

// Get retrieves a snapshot by ID.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*models.Snapshot, *SnapshotData, error) {
	snap, err := s.snapshotRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrSnapshotNotFound
		}
		return nil, nil, fmt.Errorf("snapshot get: %w", err)
	}

	var data SnapshotData
	if err := json.Unmarshal(snap.Data, &data); err != nil {
		return nil, nil, fmt.Errorf("snapshot unmarshal: %w", err)
	}

	return snap, &data, nil
}

// LinkToMergeRecord updates the snapshot row with the merge_record_id back-reference
// after the audit row is created. Must not use Create — that would INSERT a duplicate
// snapshot and leave the original row (referenced by merge_records.snapshot_id) unchanged.
func (s *Service) LinkToMergeRecord(ctx context.Context, snapshotID, mergeRecordID uuid.UUID) error {
	return s.snapshotRepo.UpdateMergeRecordID(ctx, snapshotID, mergeRecordID)
}
