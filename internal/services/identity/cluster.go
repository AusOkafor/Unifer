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
	// Union by rank
	if uf.rank[ra] < uf.rank[rb] {
		ra, rb = rb, ra
	}
	uf.parent[rb] = ra
	if uf.rank[ra] == uf.rank[rb] {
		uf.rank[ra]++
	}
}

// ScoredPair holds a pair of customer IDs, their combined similarity score,
// and the per-field component scores that produced it.
type ScoredPair struct {
	A, B         int64
	Score        float64
	EmailSim     float64
	NameSim      float64
	PhoneSim     float64
	AddressSim   float64
}

// ClusterPairs groups customer IDs into duplicate clusters using union-find.
// Only pairs with score >= threshold are joined.
// Returns a map of representative ID → list of all member IDs in the cluster.
func ClusterPairs(pairs []ScoredPair, threshold float64) map[int64][]int64 {
	uf := NewUnionFind()

	for _, p := range pairs {
		if p.Score >= threshold {
			uf.Union(p.A, p.B)
		}
	}

	// Collect clusters with > 1 member
	groups := make(map[int64][]int64)
	for id := range uf.parent {
		root := uf.Find(id)
		groups[root] = append(groups[root], id)
	}

	// Remove singleton groups (no duplicates)
	for root, members := range groups {
		if len(members) <= 1 {
			delete(groups, root)
		}
	}

	return groups
}
