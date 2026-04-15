package identity

// UnionFind implements a union-find (disjoint set) data structure for clustering.
type UnionFind struct {
	parent map[int64]int64
	rank   map[int64]int
}

func NewUnionFind() *UnionFind {
	return &UnionFind{
		parent: make(map[int64]int64),
		rank:   make(map[int64]int),
	}
}

func (uf *UnionFind) Find(id int64) int64 {
	if _, exists := uf.parent[id]; !exists {
		uf.parent[id] = id
		uf.rank[id] = 0
	}
	if uf.parent[id] != id {
		uf.parent[id] = uf.Find(uf.parent[id]) // path compression
	}
	return uf.parent[id]
}

func (uf *UnionFind) Union(a, b int64) {
	ra, rb := uf.Find(a), uf.Find(b)
	if ra == rb {
		return
	}
	if uf.rank[ra] < uf.rank[rb] {
		ra, rb = rb, ra
	}
	uf.parent[rb] = ra
	if uf.rank[ra] == uf.rank[rb] {
		uf.rank[ra]++
	}
}

// ScoredPair holds a pair of customer IDs, their combined confidence score,
// and the per-field component scores used for the UI breakdown.
type ScoredPair struct {
	A, B             int64
	Score            float64
	EmailSim         float64
	NameSim          float64
	PhoneSim         float64
	AddressSim       float64
	ConfidenceSource string // "behavioral" | "profile" | "mixed"
	Sig              Signals
}

// hasStrongSignal returns true when the pair has at least one hard identity
// anchor. Pairs without a strong signal are excluded from clustering to prevent
// weak transitive bridges from forming false-positive groups.
func hasStrongSignal(s Signals) bool {
	return s.EmailExact ||
		s.PhoneExact ||
		s.OrderAddressExact ||
		s.AddressExact ||
		(s.OrderAddressPartial && s.NameHigh) ||
		(s.EmailLocalExact && s.NameHigh) ||
		(s.PhoneSuffix && s.NameHigh)
}

// ClusterPairs groups customer IDs into duplicate clusters using union-find,
// with three additional guards that eliminate the classic union-find failure modes:
//
//  1. Threshold guard: only pairs with score ≥ threshold enter the graph.
//
//  2. Absolute-floor guard: reject any edge with score < 0.70, even if it clears
//     the threshold. This prevents high-threshold searches from accidentally
//     accepting weak transitive edges when threshold is tuned down for testing.
//
//  3. Mutual-proximity guard: only Union(A, B) when this edge score is ≥ 90%
//     of BOTH A's and B's personal best score. This kills weak transitive bridges:
//     if A's real match is C (0.95) and B tries to pull A in via a 0.66 edge,
//     the proximity check blocks it — 0.66 < 0.95 × 0.90 = 0.855.
//
// Returns a map of representative ID → list of all member IDs in the cluster.
func ClusterPairs(pairs []ScoredPair, threshold float64) map[int64][]int64 {
	// Step 1: find each node's personal best score across all pairs.
	bestScore := make(map[int64]float64)
	for _, p := range pairs {
		if p.Score > bestScore[p.A] {
			bestScore[p.A] = p.Score
		}
		if p.Score > bestScore[p.B] {
			bestScore[p.B] = p.Score
		}
	}

	uf := NewUnionFind()

	for _, p := range pairs {
		// Strong-signal guard: require at least one hard identity anchor.
		if !hasStrongSignal(p.Sig) {
			continue
		}

		// Threshold guard
		if p.Score < threshold {
			continue
		}

		// Absolute-floor guard: no edge below 0.70 ever enters the graph,
		// regardless of threshold setting. Prevents degraded behaviour when
		// threshold is tuned below this value during experiments.
		const absoluteFloor = 0.70
		if p.Score < absoluteFloor {
			continue
		}

		// Mutual-proximity guard: reject weak transitive bridges.
		// An edge is only accepted if it is a strong match for BOTH endpoints —
		// not just good enough in absolute terms, but close to each node's best.
		const proximityRatio = 0.90
		if p.Score < bestScore[p.A]*proximityRatio ||
			p.Score < bestScore[p.B]*proximityRatio {
			continue
		}

		uf.Union(p.A, p.B)
	}

	// Collect clusters with > 1 member (singletons are not duplicates).
	groups := make(map[int64][]int64)
	for id := range uf.parent {
		root := uf.Find(id)
		groups[root] = append(groups[root], id)
	}
	for root, members := range groups {
		if len(members) <= 1 {
			delete(groups, root)
		}
	}

	return groups
}

// ClusterDensity returns the ratio of actual scored pairs to the maximum
// possible pairs for a cluster of n members: density = actual / (n*(n-1)/2).
//
// A fully-corroborated cluster (every member directly matched every other)
// has density = 1.0. A star topology where all evidence routes through one
// hub node has density = 2/(n-1), which falls rapidly as n grows.
//
// Clusters with density < 0.60 are held together primarily by transitive
// bridges rather than direct corroboration — higher false-positive risk.
//
// 2-member clusters always return 1.0; they are either directly linked or absent.
func ClusterDensity(pairs []ScoredPair, memberIDs []int64) float64 {
	n := len(memberIDs)
	if n <= 2 {
		return 1.0
	}
	possible := float64(n * (n - 1) / 2)

	memberSet := make(map[int64]bool, n)
	for _, id := range memberIDs {
		memberSet[id] = true
	}

	actual := 0
	for _, p := range pairs {
		if memberSet[p.A] && memberSet[p.B] {
			actual++
		}
	}
	return float64(actual) / possible
}

// WeakestClusterEdge finds the lowest score among all scored pairs whose
// both endpoints belong to the given cluster. Returns 1.0 if the cluster
// has only one member or no edges are found (conservative: no penalty applied).
//
// Used by the risk classifier to downgrade clusters that contain at least
// one borderline pair — even if the top pair looks good, a weak interior
// link indicates the cluster may span unrelated people.
func WeakestClusterEdge(pairs []ScoredPair, memberIDs []int64) float64 {
	if len(memberIDs) <= 1 {
		return 1.0
	}
	memberSet := make(map[int64]bool, len(memberIDs))
	for _, id := range memberIDs {
		memberSet[id] = true
	}
	weakest := 1.0
	found := false
	for _, p := range pairs {
		if memberSet[p.A] && memberSet[p.B] {
			if !found || p.Score < weakest {
				weakest = p.Score
				found = true
			}
		}
	}
	if !found {
		return 1.0
	}
	return weakest
}
