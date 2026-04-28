package merge

// Unit tests for Orchestrator.Execute — no database, no Shopify API calls.
// All external dependencies are replaced with lightweight recording fakes.
//
// Invariants verified:
//  1. Snapshot is created BEFORE the Shopify merge fires.
//  2. The merge record's SnapshotID equals the ID returned by the snapshot step.
//  3. A snapshot failure aborts the pipeline before Shopify is called.
//  4. An executor failure prevents the merge record from being persisted.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/utils"
)

// ─── fakes ────────────────────────────────────────────────────────────────────

type fakeSnapshotSvc struct {
	mu          sync.Mutex
	snapID      uuid.UUID
	createErr   error
	createCalls int
	linkCalls   int
}

func (f *fakeSnapshotSvc) CreateFromCache(_ context.Context, _ uuid.UUID, _ []models.CustomerCache) (*models.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &models.Snapshot{ID: f.snapID}, nil
}

func (f *fakeSnapshotSvc) LinkToMergeRecord(_ context.Context, _, _ uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.linkCalls++
	return nil
}

// orderingSnapshotSvc wraps fakeSnapshotSvc and records a token in a shared
// slice when CreateFromCache fires — lets tests verify call ordering.
type orderingSnapshotSvc struct {
	inner *fakeSnapshotSvc
	order *[]string
	token string
}

func (o *orderingSnapshotSvc) CreateFromCache(ctx context.Context, mid uuid.UUID, custs []models.CustomerCache) (*models.Snapshot, error) {
	*o.order = append(*o.order, o.token)
	return o.inner.CreateFromCache(ctx, mid, custs)
}

func (o *orderingSnapshotSvc) LinkToMergeRecord(ctx context.Context, sid, mid uuid.UUID) error {
	return o.inner.LinkToMergeRecord(ctx, sid, mid)
}

// fakeExecutor records calls and appends an optional token for ordering tests.
type fakeExecutor struct {
	mu        sync.Mutex
	callCount int
	callErr   error
	callOrder *[]string
	token     string
}

func (f *fakeExecutor) Execute(_ context.Context, _ int64, _ []int64, _ map[string]string) (*ExecuteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.callOrder != nil {
		*f.callOrder = append(*f.callOrder, f.token)
	}
	if f.callErr != nil {
		return nil, f.callErr
	}
	return &ExecuteResult{ResultingCustomerGID: "gid://shopify/Customer/999", SecondaryCount: 1}, nil
}

// fakeMergeRepo captures every Create call.
type fakeMergeRepo struct {
	mu      sync.Mutex
	created []*models.MergeRecord
}

func (f *fakeMergeRepo) Create(_ context.Context, r *models.MergeRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ID = uuid.New()
	cp := *r
	f.created = append(f.created, &cp)
	return nil
}
func (f *fakeMergeRepo) ListByMerchant(_ context.Context, _ uuid.UUID, _, _ int) ([]models.MergeRecord, int, error) {
	return nil, 0, nil
}
func (f *fakeMergeRepo) FindByID(_ context.Context, _ uuid.UUID) (*models.MergeRecord, error) {
	return nil, nil
}
func (f *fakeMergeRepo) CountByConfidenceSource(_ context.Context, _ uuid.UUID) (*repository.ConfidenceSourceCounts, error) {
	return &repository.ConfidenceSourceCounts{}, nil
}

// fakeDuplicateRepo — no groups involved in these tests, all ops are no-ops.
type fakeDuplicateRepo struct{}

func (f *fakeDuplicateRepo) CreateGroup(_ context.Context, _ *models.DuplicateGroup) error {
	return nil
}
func (f *fakeDuplicateRepo) DeletePendingByMerchant(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeDuplicateRepo) ListByMerchant(_ context.Context, _ uuid.UUID, _ string, _ float64, _, _ int) ([]models.DuplicateGroup, int, error) {
	return nil, 0, nil
}
func (f *fakeDuplicateRepo) ListSafeGroups(_ context.Context, _ uuid.UUID) ([]models.DuplicateGroup, error) {
	return nil, nil
}
func (f *fakeDuplicateRepo) FindByID(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*models.DuplicateGroup, error) {
	return nil, errors.New("not found")
}
func (f *fakeDuplicateRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (f *fakeDuplicateRepo) TryTransitionToMerged(_ context.Context, _ uuid.UUID) (bool, error) {
	return true, nil
}
func (f *fakeDuplicateRepo) DismissGroup(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (f *fakeDuplicateRepo) ListGroupsByRiskLevels(_ context.Context, _ uuid.UUID, _ []string) ([]models.DuplicateGroup, error) {
	return nil, nil
}
func (f *fakeDuplicateRepo) MarkConfirmedByUser(_ context.Context, _ uuid.UUID, _ bool) error {
	return nil
}

// fakeCacheRepo returns a preset list of customers for FindByMerchant.
type fakeCacheRepo struct {
	customers []models.CustomerCache
}

func (f *fakeCacheRepo) FindByMerchant(_ context.Context, _ uuid.UUID) ([]models.CustomerCache, error) {
	return f.customers, nil
}
func (f *fakeCacheRepo) Upsert(_ context.Context, _ *models.CustomerCache) error { return nil }
func (f *fakeCacheRepo) FindByShopifyID(_ context.Context, _ uuid.UUID, _ int64) (*models.CustomerCache, error) {
	return nil, nil
}
func (f *fakeCacheRepo) FindByShopifyIDs(_ context.Context, _ uuid.UUID, _ []int64) ([]models.CustomerCache, error) {
	return nil, nil
}
func (f *fakeCacheRepo) DeleteByShopifyID(_ context.Context, _ uuid.UUID, _ int64) error { return nil }
func (f *fakeCacheRepo) UpdateOrderStats(_ context.Context, _ uuid.UUID, _ int64, _ int, _ string) error {
	return nil
}
func (f *fakeCacheRepo) DeleteStaleEntries(_ context.Context, _ uuid.UUID, _ []int64) (int64, error) {
	return 0, nil
}
func (f *fakeCacheRepo) CountByMerchant(_ context.Context, _ uuid.UUID) (int, error) { return 0, nil }
func (f *fakeCacheRepo) FindByMerchantAndPlatform(_ context.Context, _ uuid.UUID, _ string) ([]models.CustomerCache, error) {
	return nil, nil
}
func (f *fakeCacheRepo) FindByExternalID(_ context.Context, _ uuid.UUID, _ string, _ int64) (*models.CustomerCache, error) {
	return nil, nil
}
func (f *fakeCacheRepo) FindByExternalIDs(_ context.Context, _ uuid.UUID, _ string, _ []int64) ([]models.CustomerCache, error) {
	return nil, nil
}
func (f *fakeCacheRepo) DeleteByExternalID(_ context.Context, _ uuid.UUID, _ string, _ int64) error {
	return nil
}
func (f *fakeCacheRepo) DeleteStaleEntriesForPlatform(_ context.Context, _ uuid.UUID, _ string, _ []int64) (int64, error) {
	return 0, nil
}
func (f *fakeCacheRepo) CountByMerchantAndPlatform(_ context.Context, _ uuid.UUID, _ string) (int, error) {
	return 0, nil
}

// fakeMerchantRepo returns a preset merchant for FindByID.
type fakeMerchantRepo struct{ merchant *models.Merchant }

func (f *fakeMerchantRepo) Create(_ context.Context, _ *models.Merchant) error       { return nil }
func (f *fakeMerchantRepo) FindByDomain(_ context.Context, _ string) (*models.Merchant, error) {
	return f.merchant, nil
}
func (f *fakeMerchantRepo) FindByID(_ context.Context, _ uuid.UUID) (*models.Merchant, error) {
	return f.merchant, nil
}
func (f *fakeMerchantRepo) ListAll(_ context.Context) ([]models.Merchant, error)         { return nil, nil }
func (f *fakeMerchantRepo) UpdateToken(_ context.Context, _ uuid.UUID, _ string) error   { return nil }
func (f *fakeMerchantRepo) Delete(_ context.Context, _ uuid.UUID) error                  { return nil }

// ─── builder ─────────────────────────────────────────────────────────────────

type harness struct {
	o          *Orchestrator
	snapSvc    *fakeSnapshotSvc
	exec       *fakeExecutor
	mergeRepo  *fakeMergeRepo
	merchantID uuid.UUID
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	merchantID := uuid.New()
	snapID := uuid.New()

	snapSvc := &fakeSnapshotSvc{snapID: snapID}
	exec := &fakeExecutor{}
	mergeRepo := &fakeMergeRepo{}

	customers := []models.CustomerCache{
		{ShopifyCustomerID: 10, MerchantID: merchantID, Name: "Alice Smith", Email: "alice@example.com"},
		{ShopifyCustomerID: 11, MerchantID: merchantID, Name: "Alice Smith", Email: "alice2@example.com"},
	}

	// Use a real encryptor so Decrypt in Execute() works.
	enc, err := utils.NewEncryptor("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	require.NoError(t, err)
	encToken, err := enc.Encrypt("shpat_testtoken")
	require.NoError(t, err)

	merchant := &models.Merchant{
		ID: merchantID, ShopDomain: "test.myshopify.com", AccessTokenEnc: encToken,
	}

	o := NewOrchestrator(
		NewValidator(),
		snapSvc,
		mergeRepo,
		&fakeDuplicateRepo{},
		&fakeCacheRepo{customers: customers},
		&fakeMerchantRepo{merchant: merchant},
		enc,
		zerolog.Nop(),
	)
	// Inject the recording fake executor — no real Shopify calls.
	o.newExecutor = func(_, _ string, _ zerolog.Logger) MergeExecutor { return exec }

	return &harness{
		o: o, snapSvc: snapSvc, exec: exec, mergeRepo: mergeRepo, merchantID: merchantID,
	}
}

func (h *harness) req() MergeRequest {
	return MergeRequest{
		MerchantID:        h.merchantID,
		PrimaryCustomerID: 10,
		SecondaryIDs:      []int64{11},
		PerformedBy:       "test-user",
		Plan:              "basic", // must be basic+ to enable snapshot creation
	}
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestOrchestrator_SnapshotCreatedBeforeExecute(t *testing.T) {
	h := newHarness(t)

	var callOrder []string
	// Wrap snapshot svc to record its call position.
	h.o.snapshotSvc = &orderingSnapshotSvc{inner: h.snapSvc, order: &callOrder, token: "snapshot"}
	h.exec.callOrder = &callOrder
	h.exec.token = "execute"

	require.NoError(t, h.o.Execute(context.Background(), h.req()))

	require.Len(t, callOrder, 2)
	assert.Equal(t, "snapshot", callOrder[0], "snapshot must fire before execute")
	assert.Equal(t, "execute", callOrder[1])
}

func TestOrchestrator_MergeRecordLinkedToSnapshot(t *testing.T) {
	h := newHarness(t)

	require.NoError(t, h.o.Execute(context.Background(), h.req()))

	require.Len(t, h.mergeRepo.created, 1)
	rec := h.mergeRepo.created[0]
	require.NotNil(t, rec.SnapshotID, "merge record must reference the snapshot")
	assert.Equal(t, h.snapSvc.snapID, *rec.SnapshotID)
}

func TestOrchestrator_SnapshotFailureAbortsBeforeExecute(t *testing.T) {
	h := newHarness(t)
	h.snapSvc.createErr = errors.New("disk full")

	err := h.o.Execute(context.Background(), h.req())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "snapshot")
	assert.Equal(t, 0, h.exec.callCount, "executor must NOT be called when snapshot fails")
}

func TestOrchestrator_ExecuteFailurePreventsAuditRecord(t *testing.T) {
	h := newHarness(t)
	h.exec.callErr = errors.New("shopify: 500 internal server error")

	err := h.o.Execute(context.Background(), h.req())
	assert.Error(t, err)
	assert.Empty(t, h.mergeRepo.created, "merge record must NOT be persisted when executor fails")
}

func TestOrchestrator_SnapshotCreatedExactlyOnce(t *testing.T) {
	h := newHarness(t)

	require.NoError(t, h.o.Execute(context.Background(), h.req()))
	assert.Equal(t, 1, h.snapSvc.createCalls, "snapshot must be created exactly once per Execute call")
}
