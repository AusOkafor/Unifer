package snapshot

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	shopifysvc "merger/backend/internal/services/shopify"
)

// SnapshotData is the full pre-merge state stored in the snapshot.
type SnapshotData struct {
	Customers []shopifysvc.ShopifyCustomer `json:"customers"`
	Orders    map[int64][]shopifysvc.ShopifyOrder `json:"orders"` // keyed by shopify customer ID
}

type Service struct {
	snapshotRepo repository.SnapshotRepository
	customerSvc  *shopifysvc.CustomerService
	orderSvc     *shopifysvc.OrderService
}

func NewService(
	snapshotRepo repository.SnapshotRepository,
	customerSvc *shopifysvc.CustomerService,
	orderSvc *shopifysvc.OrderService,
) *Service {
	return &Service{
		snapshotRepo: snapshotRepo,
		customerSvc:  customerSvc,
		orderSvc:     orderSvc,
	}
}

// Create fetches full customer + order data from Shopify and stores it as a snapshot.
// This must be called BEFORE any merge — Shopify merges are irreversible.
func (s *Service) Create(ctx context.Context, merchantID uuid.UUID, customerIDs []int64) (*models.Snapshot, error) {
	data := SnapshotData{
		Orders: make(map[int64][]shopifysvc.ShopifyOrder),
	}

	for _, id := range customerIDs {
		customer, err := s.customerSvc.FetchByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("snapshot: fetch customer %d: %w", id, err)
		}
		data.Customers = append(data.Customers, *customer)

		orders, err := s.orderSvc.FetchByCustomer(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("snapshot: fetch orders for customer %d: %w", id, err)
		}
		data.Orders[id] = orders
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

// Get retrieves a snapshot by ID.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*models.Snapshot, *SnapshotData, error) {
	snap, err := s.snapshotRepo.FindByID(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot get: %w", err)
	}

	var data SnapshotData
	if err := json.Unmarshal(snap.Data, &data); err != nil {
		return nil, nil, fmt.Errorf("snapshot unmarshal: %w", err)
	}

	return snap, &data, nil
}

// LinkToMergeRecord updates the snapshot with the merge record ID after a successful merge.
func (s *Service) LinkToMergeRecord(ctx context.Context, snapshotID, mergeRecordID uuid.UUID) error {
	// Implemented via direct repo call — retrieve snap, update field, re-save
	snap, err := s.snapshotRepo.FindByID(ctx, snapshotID)
	if err != nil {
		return err
	}
	snap.MergeRecordID = &mergeRecordID
	// Re-create to update the merge_record_id (simplest for V1)
	return s.snapshotRepo.Create(ctx, snap)
}
