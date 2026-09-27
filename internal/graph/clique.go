package graph

import (
	"math/bits"
	"math/rand"
	"sort"
	"time"
)

// GreedyClique grows a clique from seed, adding the candidate with the most
// neighbors among remaining candidates (random tie-break).
func GreedyClique(g *Graph, seed []int, rng *rand.Rand) []int {
	cand := NewBitset(g.N)
	for v := 0; v < g.N; v++ {
		cand.Set(v)
	}
	var q []int
	for _, v := range seed {
		if !cand.Has(v) {
			continue
		}
		q = append(q, v)
		for i := range cand {
			cand[i] &= g.bits[v][i]
		}
	}
	for {
		best, bestScore := -1, -1
		for w, word := range cand {
			for word != 0 {
				b := bits.TrailingZeros64(word)
				word &= word - 1
				v := w*64 + b
				s := AndCount(cand, g.bits[v])*4 + rng.Intn(4)
				if s > bestScore {
					best, bestScore = v, s
				}
			}
		}
		if best < 0 {
			return q
		}
		q = append(q, best)
		for i := range cand {
			cand[i] &= g.bits[best][i]
		}
	}
}

// MaxClique runs a Tomita-style branch and bound (greedy coloring bound) until
// the deadline, returning the largest clique found. If it finishes, the clique
// is maximum.
func MaxClique(g *Graph, deadline time.Time, rng *rand.Rand) (best []int, exact bool) {
	for i := 0; i < 20; i++ {
		start := rng.Intn(g.N)
		if q := GreedyClique(g, []int{start}, rng); len(q) > len(best) {
			best = q
		}
	}
	order := make([]int, g.N)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return g.Degree(order[a]) > g.Degree(order[b]) })

	nodes := 0
	timedOut := false
	var cur []int
	var expand func(cand []int)
	expand = func(cand []int) {
		nodes++
		if nodes&255 == 0 && time.Now().After(deadline) {
			timedOut = true
		}
		if timedOut {
			return
		}
		// Greedy color cand to get upper bounds; process in reverse color order.
		colorOf, ordered := greedyColorOrder(g, cand)
		for i := len(ordered) - 1; i >= 0; i-- {
			if len(cur)+colorOf[i] <= len(best) {
				return
			}
			v := ordered[i]
			cur = append(cur, v)
			var next []int
			for _, w := range ordered[:i] {
				if g.HasEdge(v, w) {
					next = append(next, w)
				}
			}
			if len(next) == 0 {
				if len(cur) > len(best) {
					best = append([]int(nil), cur...)
				}
			} else {
				expand(next)
			}
			cur = cur[:len(cur)-1]
			if timedOut {
				return
			}
		}
	}
	expand(order)
	sort.Ints(best)
	return best, !timedOut
}

// greedyColorOrder sequentially colors cand and returns vertices sorted by
// color along with the color number (1-based) at each position.
func greedyColorOrder(g *Graph, cand []int) ([]int, []int) {
	var classes [][]int
	for _, v := range cand {
		placed := false
		for ci, cl := range classes {
			ok := true
			for _, w := range cl {
				if g.HasEdge(v, w) {
					ok = false
					break
				}
			}
			if ok {
				classes[ci] = append(cl, v)
				placed = true
				break
			}
		}
		if !placed {
			classes = append(classes, []int{v})
		}
	}
	colorOf := make([]int, 0, len(cand))
	ordered := make([]int, 0, len(cand))
	for ci, cl := range classes {
		for _, v := range cl {
			ordered = append(ordered, v)
			colorOf = append(colorOf, ci+1)
		}
	}
	return colorOf, ordered
}
