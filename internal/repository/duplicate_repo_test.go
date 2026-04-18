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

// ─── helpers ─────────────────────────────────────────────────────────────────

func newGroup(merchantID uuid.UUID, hash string, status string) *models.DuplicateGroup {
	return &models.DuplicateGroup{
		MerchantID:      merchantID,
		GroupHash:       hash,
		CustomerIDs:     pq.Int64Array{1, 2},
		ConfidenceScore: 0.85,
		Status:          status,
	}
}

// ─── CreateGroup / upsert behaviour ──────────────────────────────────────────

func TestDuplicateRepo_DefaultStatusIsPending(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "test.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewDuplicateRepo(db)
	g := newGroup(merchantID, "hash-default", "pending")
	require.NoError(t, repo.CreateGroup(ctx, g))

	var status string
	require.NoError(t, db.GetContext(ctx, &status,
		`SELECT status FROM duplicate_groups WHERE group_hash = $1`, "hash-default"))
	assert.Equal(t, "pending", status)
}

func TestDuplicateRepo_UpsertPreservesReviewedStatus(t *testing.T) {
	// Re-running detection (same hash) must NOT overwrite a manually-reviewed group
	// back to pending. The partial-index conflict clause only updates score/data
	// fields — status is excluded.
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "test2.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewDuplicateRepo(db)

	// Step 1: initial detection inserts as pending.
	g := newGroup(merchantID, "hash-reviewed", "pending")
	require.NoError(t, repo.CreateGroup(ctx, g))

	// Step 2: user reviews the group.
	_, err = db.ExecContext(ctx,
		`UPDATE duplicate_groups SET status = 'reviewed' WHERE group_hash = $1`, "hash-reviewed")
	require.NoError(t, err)

	// Step 3: re-run detection — same hash, updated confidence.
	g2 := newGroup(merchantID, "hash-reviewed", "pending")
	g2.ConfidenceScore = 0.90
	require.NoError(t, repo.CreateGroup(ctx, g2))

	var status string
	require.NoError(t, db.GetContext(ctx, &status,
		`SELECT status FROM duplicate_groups WHERE group_hash = $1`, "hash-reviewed"))
	assert.Equal(t, "reviewed", status, "upsert must not revert status from reviewed → pending")

	// Confidence should have been updated.
	var conf float64
	require.NoError(t, db.GetContext(ctx, &conf,
		`SELECT confidence_score FROM duplicate_groups WHERE group_hash = $1`, "hash-reviewed"))
	assert.InDelta(t, 0.90, conf, 0.001, "upsert must refresh confidence_score")
}

func TestDuplicateRepo_UpsertCreatesNewRowAfterMerge(t *testing.T) {
	// When a group is merged its status = 'merged'. The partial index
	// WHERE status != 'merged' becomes inactive for that row, so a subsequent
	// upsert with the same hash inserts a brand-new pending row rather than
	// updating the merged one. This is the intended fresh-detection behaviour.
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "test3.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewDuplicateRepo(db)

	// Step 1: create and immediately mark as merged.
	g := newGroup(merchantID, "hash-merged", "pending")
	require.NoError(t, repo.CreateGroup(ctx, g))
	mergedGroupID := g.ID

	ok, err := repo.TryTransitionToMerged(ctx, mergedGroupID)
	require.NoError(t, err)
	require.True(t, ok)

	// Step 2: re-run detection finds the same pair again.
	g2 := newGroup(merchantID, "hash-merged", "pending")
	require.NoError(t, repo.CreateGroup(ctx, g2))

	// Two rows now exist — one merged, one new pending.
	var count int
	require.NoError(t, db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM duplicate_groups WHERE merchant_id = $1 AND group_hash = $2`,
		merchantID, "hash-merged"))
	assert.Equal(t, 2, count, "second detection after merge must create a new pending row")

	// The original merged row must not have been touched.
	var mergedStatus string
	require.NoError(t, db.GetContext(ctx, &mergedStatus,
		`SELECT status FROM duplicate_groups WHERE id = $1`, mergedGroupID))
	assert.Equal(t, "merged", mergedStatus, "original merged row must remain merged")
}

// ─── TryTransitionToMerged ────────────────────────────────────────────────────

func TestDuplicateRepo_TryTransitionToMerged_FirstCallerSucceeds(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "test4.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewDuplicateRepo(db)
	g := newGroup(merchantID, "hash-transition", "pending")
	require.NoError(t, repo.CreateGroup(ctx, g))

	ok, err := repo.TryTransitionToMerged(ctx, g.ID)
	require.NoError(t, err)
	assert.True(t, ok, "first call must succeed")

	var status string
	require.NoError(t, db.GetContext(ctx, &status,
		`SELECT status FROM duplicate_groups WHERE id = $1`, g.ID))
	assert.Equal(t, "merged", status)
}

func TestDuplicateRepo_TryTransitionToMerged_SecondCallerFails(t *testing.T) {
	// Simulates concurrent double-merge: second TryTransitionToMerged returns ok=false.
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "test5.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewDuplicateRepo(db)
	g := newGroup(merchantID, "hash-double", "pending")
	require.NoError(t, repo.CreateGroup(ctx, g))

	// First caller merges successfully.
	ok1, err := repo.TryTransitionToMerged(ctx, g.ID)
	require.NoError(t, err)
	require.True(t, ok1)

	// Second caller — same group, already merged.
	ok2, err := repo.TryTransitionToMerged(ctx, g.ID)
	require.NoError(t, err)
	assert.False(t, ok2, "second call on already-merged group must return ok=false")
}

// ─── DismissGroup ─────────────────────────────────────────────────────────────

func TestDuplicateRepo_DismissGroupSetsStatus(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "test6.myshopify.com", "enc")
	require.NoError(t, err)

	repo := repository.NewDuplicateRepo(db)
	g := newGroup(merchantID, "hash-dismiss", "pending")
	require.NoError(t, repo.CreateGroup(ctx, g))

	require.NoError(t, repo.DismissGroup(ctx, g.ID, "not a real duplicate"))

	var status, reason string
	require.NoError(t, db.GetContext(ctx, &status,
		`SELECT status FROM duplicate_groups WHERE id = $1`, g.ID))
	require.NoError(t, db.GetContext(ctx, &reason,
		`SELECT dismiss_reason FROM duplicate_groups WHERE id = $1`, g.ID))
	assert.Equal(t, "dismissed", status)
	assert.Equal(t, "not a real duplicate", reason)
}
