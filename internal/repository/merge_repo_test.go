package repository_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/testhelper"
)

func newMergeRecord(merchantID uuid.UUID, primaryID int64) *models.MergeRecord {
	return &models.MergeRecord{
		MerchantID:           merchantID,
		PrimaryCustomerID:    primaryID,
		SecondaryCustomerIDs: pq.Int64Array{primaryID + 100},
		OrdersMoved:          0,
		PerformedBy:          "test-user",
	}
}

// ─── Create ───────────────────────────────────────────────────────────────────

func TestMergeRepo_Create_AssignsIDAndCreatedAt(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "mrepo1.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewMergeRepo(db)
	rec := newMergeRecord(merchantID, 10)
	require.NoError(t, repo.Create(ctx, rec))

	assert.NotEqual(t, uuid.Nil, rec.ID, "Create must populate rec.ID via RETURNING")
	assert.False(t, rec.CreatedAt.IsZero(), "Create must populate rec.CreatedAt via RETURNING")
}

func TestMergeRepo_AppendOnly(t *testing.T) {
	// merge_records has no upsert — two Create calls for the same primary must
	// produce two distinct rows with different IDs.
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "mrepo2.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewMergeRepo(db)
	rec1 := newMergeRecord(merchantID, 20)
	rec2 := newMergeRecord(merchantID, 20) // same primary, different record
	require.NoError(t, repo.Create(ctx, rec1))
	require.NoError(t, repo.Create(ctx, rec2))

	assert.NotEqual(t, rec1.ID, rec2.ID, "each Create must produce a unique ID")

	var count int
	require.NoError(t, db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM merge_records WHERE primary_customer_id = 20 AND merchant_id = $1`,
		merchantID))
	assert.Equal(t, 2, count, "merge_records must be append-only (two rows expected)")
}

// ─── OverrideUsed + ConfidenceSource ─────────────────────────────────────────

func TestMergeRepo_OverrideUsedPersistedTrue(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "mrepo3.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewMergeRepo(db)
	rec := newMergeRecord(merchantID, 30)
	rec.OverrideUsed = true
	require.NoError(t, repo.Create(ctx, rec))

	out, err := repo.FindByID(ctx, rec.ID)
	require.NoError(t, err)
	assert.True(t, out.OverrideUsed, "override_used=true must round-trip through the DB")
}

func TestMergeRepo_OverrideUsedPersistedFalse(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "mrepo4.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewMergeRepo(db)
	rec := newMergeRecord(merchantID, 40)
	rec.OverrideUsed = false
	require.NoError(t, repo.Create(ctx, rec))

	out, err := repo.FindByID(ctx, rec.ID)
	require.NoError(t, err)
	assert.False(t, out.OverrideUsed, "override_used=false must round-trip through the DB")
}

func TestMergeRepo_ConfidenceSourceBehavioral(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "mrepo5.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewMergeRepo(db)
	rec := newMergeRecord(merchantID, 50)
	rec.ConfidenceSource = "behavioral"
	require.NoError(t, repo.Create(ctx, rec))

	out, err := repo.FindByID(ctx, rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "behavioral", out.ConfidenceSource)
}

func TestMergeRepo_ConfidenceSourceProfile(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "mrepo6.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewMergeRepo(db)
	rec := newMergeRecord(merchantID, 60)
	rec.ConfidenceSource = "profile"
	require.NoError(t, repo.Create(ctx, rec))

	out, err := repo.FindByID(ctx, rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "profile", out.ConfidenceSource)
}

// ─── ListByMerchant ───────────────────────────────────────────────────────────

func TestMergeRepo_ListByMerchant_ReturnsOnlyOwnMerchant(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	m1 := uuid.New()
	m2 := uuid.New()
	for _, m := range []struct {
		id     uuid.UUID
		domain string
	}{{m1, "list1.myshopify.com"}, {m2, "list2.myshopify.com"}} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
			m.id, m.domain, "enc")
		require.NoError(t, err)
	}

	repo := repository.NewMergeRepo(db)
	// Two records for m1, one for m2.
	require.NoError(t, repo.Create(ctx, newMergeRecord(m1, 70)))
	require.NoError(t, repo.Create(ctx, newMergeRecord(m1, 71)))
	require.NoError(t, repo.Create(ctx, newMergeRecord(m2, 72)))

	records, total, err := repo.ListByMerchant(ctx, m1, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, records, 2)
	for _, r := range records {
		assert.Equal(t, m1, r.MerchantID, "list must only return records for the requested merchant")
	}
}

// ─── CountByConfidenceSource ─────────────────────────────────────────────────

func TestMergeRepo_CountByConfidenceSource(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "counts.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewMergeRepo(db)

	r1 := newMergeRecord(merchantID, 80)
	r1.ConfidenceSource = "behavioral"
	require.NoError(t, repo.Create(ctx, r1))

	r2 := newMergeRecord(merchantID, 81)
	r2.ConfidenceSource = "profile"
	require.NoError(t, repo.Create(ctx, r2))

	r3 := newMergeRecord(merchantID, 82)
	r3.ConfidenceSource = "mixed"
	r3.OverrideUsed = true
	require.NoError(t, repo.Create(ctx, r3))

	counts, err := repo.CountByConfidenceSource(ctx, merchantID)
	require.NoError(t, err)
	assert.Equal(t, 1, counts.Behavioral)
	assert.Equal(t, 1, counts.Profile)
	assert.Equal(t, 1, counts.Mixed)
	assert.Equal(t, 1, counts.Override)
}
