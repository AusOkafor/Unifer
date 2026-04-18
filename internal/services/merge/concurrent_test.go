package merge_test

// Integration test: concurrent merge protection via TryTransitionToMerged.
//
// Two goroutines race to mark the same duplicate group as merged.
// The DB-level atomic UPDATE (WHERE status != 'merged') guarantees exactly
// one caller gets rows-affected=1 (success) and the other gets 0 (already merged).
// This prevents double-merge records even under high concurrency.
//
// Requires TEST_DATABASE_URL — skipped automatically when not set.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"merger/backend/internal/repository"
	"merger/backend/internal/testhelper"
)

func TestConcurrentMerge_TryTransitionToMergedOnlyOneSucceeds(t *testing.T) {
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()

	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "race.myshopify.com", "enc")
	require.NoError(t, err)

	groupID := uuid.New()
	_, err = db.ExecContext(ctx,
		`INSERT INTO duplicate_groups (id, merchant_id, group_hash, customer_ids, confidence_score, status)
		 VALUES ($1,$2,$3,$4,0.92,'pending')`,
		groupID, merchantID, "race-hash", pq.Array([]int64{1, 2}))
	require.NoError(t, err)

	repo := repository.NewDuplicateRepo(db)

	// Fire N concurrent attempts — each tries to atomically claim the merge.
	const workers = 8
	var (
		wg       sync.WaitGroup
		successes int32
		failures  int32
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ok, err := repo.TryTransitionToMerged(context.Background(), groupID)
			if err != nil {
				t.Errorf("worker %d: unexpected error: %v", n, err)
				return
			}
			if ok {
				atomic.AddInt32(&successes, 1)
			} else {
				atomic.AddInt32(&failures, 1)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int32(1), successes,
		"exactly one concurrent caller must claim the merge (got %d)", successes)
	assert.Equal(t, int32(workers-1), failures,
		"all other callers must get ok=false (got %d)", failures)

	// Verify the group is now permanently merged.
	var status string
	require.NoError(t, db.GetContext(ctx, &status,
		`SELECT status FROM duplicate_groups WHERE id = $1`, groupID))
	assert.Equal(t, "merged", status)
}

func TestConcurrentMerge_MultipleGroupsIndependent(t *testing.T) {
	// Each group has its own race — cross-group interference must not occur.
	db := testhelper.OpenTestDB(t)
	testhelper.MustRunMigrations(t, db)
	testhelper.TruncateAll(t, db)

	ctx := context.Background()
	merchantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO merchants (id, shop_domain, access_token_enc) VALUES ($1,$2,$3)`,
		merchantID, "race2.myshopify.com", "enc")
	require.NoError(t, err)

	const numGroups = 5
	groupIDs := make([]uuid.UUID, numGroups)
	for i := 0; i < numGroups; i++ {
		groupIDs[i] = uuid.New()
		_, err = db.ExecContext(ctx,
			`INSERT INTO duplicate_groups (id, merchant_id, group_hash, customer_ids, confidence_score, status)
			 VALUES ($1,$2,$3,$4,0.85,'pending')`,
			groupIDs[i], merchantID, fmt.Sprintf("multi-hash-%d", i), pq.Array([]int64{int64(i*10 + 1), int64(i*10 + 2)}))
		require.NoError(t, err)
	}

	repo := repository.NewDuplicateRepo(db)

	// For each group, fire 3 concurrent merge attempts.
	const racePerGroup = 3
	var wg sync.WaitGroup
	successPerGroup := make([]int32, numGroups)

	for g, gid := range groupIDs {
		for r := 0; r < racePerGroup; r++ {
			wg.Add(1)
			go func(g int, gid uuid.UUID) {
				defer wg.Done()
				ok, err := repo.TryTransitionToMerged(context.Background(), gid)
				if err == nil && ok {
					atomic.AddInt32(&successPerGroup[g], 1)
				}
			}(g, gid)
		}
	}
	wg.Wait()

	for i, count := range successPerGroup {
		assert.Equal(t, int32(1), count,
			"group %d: exactly one merge must succeed (got %d)", i, count)
	}
}
