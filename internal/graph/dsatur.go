package graph

import (
	"math/rand"
	"time"
)

// DSatur greedily colors g: repeatedly color the vertex with the most distinct
// neighbor colors (ties: highest degree, then random) with its lowest free color.
func DSatur(g *Graph, rng *rand.Rand) []int {
	n := g.N
	col := make([]int, n)
	for i := range col {
		col[i] = -1
	}
	// satur[v] is a set of neighbor colors, kept as a map-free bitset per vertex.
	words := (n + 64) / 64
	sat := make([][]uint64, n)
	satCount := make([]int, n)
	for i := range sat {
		sat[i] = make([]uint64, words)
	}
	noise := make([]float64, n)
	if rng != nil {
		for i := range noise {
			noise[i] = rng.Float64()
		}
	}
	for step := 0; step < n; step++ {
		best := -1
		for v := 0; v < n; v++ {
			if col[v] >= 0 {
				continue
			}
			if best < 0 || satCount[v] > satCount[best] ||
				(satCount[v] == satCount[best] && (g.Degree(v) > g.Degree(best) ||
					(g.Degree(v) == g.Degree(best) && noise[v] > noise[best]))) {
				best = v
			}
		}
		c := 0
		for sat[best][c>>6]&(1<<(uint(c)&63)) != 0 {
			c++
		}
		col[best] = c
		for _, w := range g.Adj[best] {
			if col[w] < 0 && sat[w][c>>6]&(1<<(uint(c)&63)) == 0 {
				sat[w][c>>6] |= 1 << (uint(c) & 63)
				satCount[w]++
			}
		}
	}
	return col
}

// Status is the outcome of a decision procedure.
type Status int

const (
	Unknown Status = iota
	Sat
	Unsat
)

func (s Status) String() string {
	switch s {
	case Sat:
		return "sat"
	case Unsat:
		return "unsat"
	}
	return "unknown"
}

// Colorable decides whether g has a proper k-coloring using DSatur
// branch-and-bound with color-symmetry breaking. It is the in-process
// counterpart of the Z3 check: agents use it to search for small witness
// subgraphs, and it gives the server a fallback when z3 is not installed.
func Colorable(g *Graph, k int, deadline time.Time) (Status, []int) {
	n := g.N
	if n == 0 {
		return Sat, nil
	}
	if k <= 0 {
		return Unsat, nil
	}
	if k > 64 {
		// Domains are uint64 masks; DSatur's greedy bound settles large k in practice.
		col := DSatur(g, nil)
		if NumColors(col) <= k {
			return Sat, col
		}
		return Unknown, nil
	}
	col := make([]int, n)
	for i := range col {
		col[i] = -1
	}
	// cnt[v*k+c] = number of colored neighbors of v with color c.
	cnt := make([]int32, n*k)
	forb := make([]uint64, n) // mask of colors blocked at v
	full := uint64(1)<<uint(k) - 1
	if k == 64 {
		full = ^uint64(0)
	}
	nodes := 0
	timedOut := false

	assign := func(v, c int) {
		col[v] = c
		for _, w := range g.Adj[v] {
			i := w*k + c
			cnt[i]++
			if cnt[i] == 1 {
				forb[w] |= 1 << uint(c)
			}
		}
	}
	unassign := func(v int) {
		c := col[v]
		col[v] = -1
		for _, w := range g.Adj[v] {
			i := w*k + c
			cnt[i]--
			if cnt[i] == 0 {
				forb[w] &^= 1 << uint(c)
			}
		}
	}
	popcnt := func(x uint64) int {
		c := 0
		for x != 0 {
			x &= x - 1
			c++
		}
		return c
	}

	var rec func(colored, maxUsed int) bool
	rec = func(colored, maxUsed int) bool {
		if colored == n {
			return true
		}
		nodes++
		if nodes&1023 == 0 && time.Now().After(deadline) {
			timedOut = true
		}
		if timedOut {
			return false
		}
		// Pick the uncolored vertex with the fewest available colors.
		best, bestFree, bestDeg := -1, 1<<30, -1
		for v := 0; v < n; v++ {
			if col[v] >= 0 {
				continue
			}
			free := popcnt(full &^ forb[v])
			if free == 0 {
				return false
			}
			d := g.Degree(v)
			if free < bestFree || (free == bestFree && d > bestDeg) {
				best, bestFree, bestDeg = v, free, d
			}
		}
		avail := full &^ forb[best]
		for c := 0; c < k; c++ {
			if avail&(1<<uint(c)) == 0 {
				continue
			}
			// Colors above maxUsed+1 are interchangeable; try only the first.
			if c > maxUsed+1 {
				break
			}
			assign(best, c)
			nm := maxUsed
			if c > nm {
				nm = c
			}
			if rec(colored+1, nm) {
				return true
			}
			unassign(best)
			if timedOut {
				return false
			}
		}
		return false
	}
	if rec(0, -1) {
		return Sat, append([]int(nil), col...)
	}
	if timedOut {
		return Unknown, nil
	}
	return Unsat, nil
}
