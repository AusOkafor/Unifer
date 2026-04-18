package identity

// Tests for hasStrongSignal() and ClusterPairs() guards.
// Uses package identity (not identity_test) to access unexported hasStrongSignal.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─── hasStrongSignal ─────────────────────────────────────────────────────
// ─────

func TestHasStrongSignal_HardAnchors(t *testing.T) {
	assert.True(t, hasStrongSignal(Signals{EmailExact: true}), "EmailExact is a hard anchor")
	assert.True(t, hasStrongSignal(Signals{PhoneExact: true}), "PhoneExact is a hard anchor")
	assert.True(t, hasStrongSignal(Signals{AddressExact: true}), "AddressExact is a hard anchor")
	assert.True(t, hasStrongSignal(Signals{OrderAddressExact: true}), "OrderAddressExact is a hard anchor")
}

func TestHasStrongSignal_CompoundAnchors(t *testing.T) {
	assert.True(t,
		hasStrongSignal(Signals{OrderAddressPartial: true, NameHigh: true}),
		"OrderAddressPartial+NameHigh qualifies as a strong signal",
	)
	assert.True(t,
		hasStrongSignal(Signals{EmailLocalExact: true, NameHigh: true}),
		"EmailLocalExact+NameHigh qualifies as a strong signal",
	)
	assert.True(t,
		hasStrongSignal(Signals{PhoneSuffix: true, NameHigh: true}),
		"PhoneSuffix+NameHigh qualifies as a strong signal",
	)
}

func TestHasStrongSignal_WeakSignalsFalse(t *testing.T) {
	assert.False(t, hasStrongSignal(Signals{NameHigh: true}), "NameHigh alone is not a strong signal")
	assert.False(t, hasStrongSignal(Signals{AddressPartial: true}), "AddressPartial alone is not a strong signal")
	assert.False(t, hasStrongSignal(Signals{NameHigh: true, AddressPartial: true}),
		"NameHigh+AddressPartial is not a strong signal (AddressPartial ≠ AddressExact)")
	assert.False(t, hasStrongSignal(Signals{EmailLocalFuzzy: true, NameMedium: true}),
		"EmailLocalFuzzy+NameMedium is not a strong signal")
	assert.False(t, hasStrongSignal(Signals{NameHigh: true, EmailDomainMatch: true}),
		"NameHigh+EmailDomainMatch is not a strong signal")
	assert.False(t, hasStrongSignal(Signals{}), "empty signals returns false")
}

func TestHasStrongSignal_CompoundRequiresBothParts(t *testing.T) {
	// OrderAddressPartial without NameHigh is not enough.
	assert.False(t, hasStrongSignal(Signals{OrderAddressPartial: true, NameMedium: true}),
		"OrderAddressPartial+NameMedium is not a strong signal (requires NameHigh)")
	// EmailLocalExact without NameHigh is not enough.
	assert.False(t, hasStrongSignal(Signals{EmailLocalExact: true}),
		"EmailLocalExact alone is not a strong signal")
	// PhoneSuffix without NameHigh is not enough.
	assert.False(t, hasStrongSignal(Signals{PhoneSuffix: true}),
		"PhoneSuffix alone is not a strong signal")
}

// ─── ClusterPairs ─────────────────────────────────────────────────────────────

func TestClusterPairs_WeakSignalDoesNotCluster(t *testing.T) {
	// NameHigh + AddressPartial scores 0.76 but has no hard anchor —
	// hasStrongSignal returns false, pair is rejected before threshold check.
	pairs := []ScoredPair{{
		A:     1,
		B:     2,
		Score: 0.76,
		Sig:   Signals{NameHigh: true, AddressPartial: true, SameCountry: true},
	}}
	clusters := ClusterPairs(pairs, DefaultThreshold)
	assert.Empty(t, clusters, "name-only pair must not form a cluster")
}

func TestClusterPairs_StrongSignalClusters(t *testing.T) {
	pairs := []ScoredPair{{
		A:     1,
		B:     2,
		Score: 0.92,
		Sig:   Signals{EmailExact: true, NameHigh: true, SameCountry: true},
	}}
	clusters := ClusterPairs(pairs, DefaultThreshold)
	assert.Len(t, clusters, 1, "EmailExact pair above threshold must form a cluster")
}

func TestClusterPairs_CompoundSignalClusters(t *testing.T) {
	// OrderAddressPartial+NameHigh is a valid compound strong signal.
	pairs := []ScoredPair{{
		A:     1,
		B:     2,
		Score: 0.90,
		Sig:   Signals{OrderAddressPartial: true, NameHigh: true, SameCountry: true},
	}}
	clusters := ClusterPairs(pairs, DefaultThreshold)
	assert.Len(t, clusters, 1, "OrderAddressPartial+NameHigh pair must form a cluster")
}

func TestClusterPairs_AbsoluteFloor(t *testing.T) {
	// Score 0.65 passes DefaultThreshold (0.65) but is below the absolute floor (0.70).
	// Even with a hard anchor, the pair must not cluster.
	pairs := []ScoredPair{{
		A:     1,
		B:     2,
		Score: 0.65,
		Sig:   Signals{EmailExact: true, SameCountry: true},
	}}
	clusters := ClusterPairs(pairs, DefaultThreshold)
	assert.Empty(t, clusters, "score at absolute floor (0.65 < 0.70) must not cluster")
}

func TestClusterPairs_AbsoluteFloorBoundary(t *testing.T) {
	// Score exactly at 0.70 should be accepted (floor is strictly <, not <=).
	pairs := []ScoredPair{{
		A:     1,
		B:     2,
		Score: 0.70,
		Sig:   Signals{EmailExact: true, SameCountry: true},
	}}
	clusters := ClusterPairs(pairs, DefaultThreshold)
	assert.Len(t, clusters, 1, "score exactly at 0.70 must cluster (floor is < 0.70)")
}

func TestClusterPairs_MutualProximityGuard(t *testing.T) {
	// A has best score 0.95 (against C). B tries to pull A in via 0.71.
	// 0.71 < 0.95 × 0.90 = 0.855 — proximity guard must block B.
	// bestScore is pre-computed across all pairs before any union, so order
	// of iteration does not affect the result.
	pairs := []ScoredPair{
		{A: 1, B: 3, Score: 0.95, Sig: Signals{EmailExact: true, SameCountry: true}},
		{A: 1, B: 2, Score: 0.71, Sig: Signals{EmailExact: true, SameCountry: true}},
	}
	clusters := ClusterPairs(pairs, DefaultThreshold)

	// A-C must cluster (both have best=0.95, 0.95 ≥ 0.95×0.90=0.855).
	// B must not be included in any cluster.
	foundB := false
	for _, members := range clusters {
		for _, id := range members {
			if id == int64(2) {
				foundB = true
			}
		}
	}
	assert.False(t, foundB, "weak transitive bridge (B) must be rejected by mutual proximity guard")
}

func TestClusterPairs_MutualProximityGuardBothSides(t *testing.T) {
	// A and C both have high best scores against other partners.
	// The A-C edge (0.80) may not meet both sides' proximity requirements.
	// A's best: 0.95 (from A-D), requires edge ≥ 0.95×0.90=0.855
	// 0.80 < 0.855 → A-C rejected.
	pairs := []ScoredPair{
		{A: 1, B: 4, Score: 0.95, Sig: Signals{EmailExact: true, SameCountry: true}},
		{A: 1, B: 3, Score: 0.80, Sig: Signals{EmailExact: true, SameCountry: true}},
	}
	clusters := ClusterPairs(pairs, DefaultThreshold)

	foundThree := false
	for _, members := range clusters {
		for _, id := range members {
			if id == int64(3) {
				foundThree = true
			}
		}
	}
	assert.False(t, foundThree,
		"A-C edge rejected because it falls below A's proximity requirement (0.80 < 0.95×0.90)")
}

func TestClusterPairs_MultipleIndependentClusters(t *testing.T) {
	// Two independent well-corroborated pairs form two distinct clusters.
	pairs := []ScoredPair{
		{A: 1, B: 2, Score: 0.92, Sig: Signals{EmailExact: true, SameCountry: true}},
		{A: 3, B: 4, Score: 0.88, Sig: Signals{PhoneExact: true, SameCountry: true}},
	}
	clusters := ClusterPairs(pairs, DefaultThreshold)
	assert.Len(t, clusters, 2, "two independent strongly-corroborated pairs must form two clusters")
}

func TestClusterPairs_EmptyInputReturnsEmpty(t *testing.T) {
	clusters := ClusterPairs(nil, DefaultThreshold)
	assert.Empty(t, clusters)
}

func TestClusterPairs_BelowThresholdNotClustered(t *testing.T) {
	// Score 0.60 is below DefaultThreshold (0.65) — rejected by threshold guard.
	pairs := []ScoredPair{{
		A:     1,
		B:     2,
		Score: 0.60,
		Sig:   Signals{EmailExact: true, SameCountry: true},
	}}
	clusters := ClusterPairs(pairs, DefaultThreshold)
	assert.Empty(t, clusters, "score below DefaultThreshold must not cluster")
}
