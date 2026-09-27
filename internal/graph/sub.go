package graph

import (
	"fmt"
	"sort"
)

// Sub is a small induced subgraph G[S] (plus derived edges, plus an optional
// u=v contraction) relabeled to local indices 0..n-1. It is what the Z3
// harness and the exact solver actually see, so it stays at 20-60 vertices.
type Sub struct {
	G     *Graph // local graph
	Orig  []int  // local index -> original vertex id
	Local map[int]int
	// Merged is the local index of the contracted u=v vertex, or -1.
	Merged int
}

// SubOpts configures how a certificate subgraph is built.
type SubOpts struct {
	Extra [][2]int // additional edges known to hold for every relevant coloring (verified separations)
	Merge *[2]int  // contract u and v (encodes c(u) = c(v))
}

// ErrTrivialMerge means u and v are adjacent, so c(u)=c(v) is impossible outright.
var ErrTrivialMerge = fmt.Errorf("u and v are adjacent; separation is trivial")

// Induce builds G[S] with extra edges and optional contraction. S is
// deduplicated and sorted so the result is canonical for a given input set.
func Induce(g *Graph, S []int, o SubOpts) (*Sub, error) {
	set := make(map[int]bool, len(S))
	vs := make([]int, 0, len(S))
	for _, v := range S {
		if v < 0 || v >= g.N {
			return nil, fmt.Errorf("vertex %d out of range", v)
		}
		if !set[v] {
			set[v] = true
			vs = append(vs, v)
		}
	}
	if o.Merge != nil {
		for _, v := range o.Merge {
			if !set[v] {
				set[v] = true
				vs = append(vs, v)
			}
		}
	}
	sort.Ints(vs)

	extra := make(map[[2]int]bool)
	for _, e := range o.Extra {
		a, b := e[0], e[1]
		if a > b {
			a, b = b, a
		}
		if set[a] && set[b] && a != b {
			extra[[2]int{a, b}] = true
		}
	}
	adjacent := func(a, b int) bool {
		if a > b {
			a, b = b, a
		}
		return g.HasEdge(a, b) || extra[[2]int{a, b}]
	}

	// Map original -> local; the merged vertex v collapses onto u.
	mu, mv := -1, -1
	if o.Merge != nil {
		mu, mv = o.Merge[0], o.Merge[1]
		if mu == mv {
			return nil, fmt.Errorf("merge of a vertex with itself")
		}
		if adjacent(mu, mv) {
			return nil, ErrTrivialMerge
		}
	}
	local := make(map[int]int, len(vs))
	var orig []int
	for _, v := range vs {
		if v == mv {
			continue
		}
		local[v] = len(orig)
		orig = append(orig, v)
	}
	if mv >= 0 {
		local[mv] = local[mu]
	}
	var edges [][2]int
	for i := 0; i < len(vs); i++ {
		for j := i + 1; j < len(vs); j++ {
			a, b := vs[i], vs[j]
			if adjacent(a, b) {
				la, lb := local[a], local[b]
				if la != lb {
					edges = append(edges, [2]int{la, lb})
				}
			}
		}
	}
	s := &Sub{G: New("sub", len(orig), edges), Orig: orig, Local: local, Merged: -1}
	if mu >= 0 {
		s.Merged = local[mu]
	}
	return s, nil
}

// Core returns the local vertices that survive iterated removal of vertices
// with degree < k. Removed vertices can always be colored last with k colors,
// so G is k-colorable iff its k-core is. Sound, and it shrinks what Z3 sees.
func Core(g *Graph, k int) []int {
	core, _ := CoreOrder(g, k)
	return core
}

// CoreOrder is Core plus the removal order of pruned vertices. Coloring the
// removed vertices greedily in reverse order always succeeds with k colors,
// which turns a coloring of the core into a coloring of the whole graph.
func CoreOrder(g *Graph, k int) ([]int, []int) {
	deg := make([]int, g.N)
	alive := make([]bool, g.N)
	var queue, removed []int
	for v := 0; v < g.N; v++ {
		deg[v] = g.Degree(v)
		alive[v] = true
		if deg[v] < k {
			queue = append(queue, v)
			alive[v] = false
		}
	}
	for len(queue) > 0 {
		v := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		removed = append(removed, v)
		for _, w := range g.Adj[v] {
			if alive[w] {
				deg[w]--
				if deg[w] < k {
					alive[w] = false
					queue = append(queue, w)
				}
			}
		}
	}
	var out []int
	for v := 0; v < g.N; v++ {
		if alive[v] {
			out = append(out, v)
		}
	}
	return out, removed
}

// ExtendColoring colors the vertices in removed (reverse order) greedily,
// given col with the core already colored (-1 = uncolored).
func ExtendColoring(g *Graph, col []int, removed []int) {
	for i := len(removed) - 1; i >= 0; i-- {
		v := removed[i]
		used := map[int]bool{}
		for _, w := range g.Adj[v] {
			if col[w] >= 0 {
				used[col[w]] = true
			}
		}
		c := 0
		for used[c] {
			c++
		}
		col[v] = c
	}
}

// InducedLocal restricts a (local) graph to the given vertex list, relabeling.
func InducedLocal(g *Graph, keep []int) (*Graph, []int) {
	idx := make(map[int]int, len(keep))
	for i, v := range keep {
		idx[v] = i
	}
	var edges [][2]int
	for _, u := range keep {
		for _, v := range g.Adj[u] {
			if j, ok := idx[v]; ok && idx[u] < j {
				edges = append(edges, [2]int{idx[u], j})
			}
		}
	}
	return New(g.Name, len(keep), edges), keep
}
