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
	A, B       int64
	Score      float64
	EmailSim   float64
	NameSim    float64
	PhoneSim   float64
	AddressSim float64
}

// ClusterPairs groups customer IDs into duplicate clusters using union-find,
// with two additional guards that eliminate the classic union-find failure modes:
//
//  1. Threshold guard: only pairs with score ≥ threshold enter the graph.
//
//  2. Mutual-proximity guard: only Union(A, B) when this edge score is ≥ 90%
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
		// Threshold guard
		if p.Score < threshold {
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
