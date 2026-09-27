package graph

import (
	"math/rand"
)

// TabuOpts controls a Tabucol run.
type TabuOpts struct {
	K        int
	MaxIters int
	Rng      *rand.Rand
	Stop     func() bool // polled every 1024 iterations
	// Pinned vertices keep their initial color (e.g. an LLM-chosen anchor).
	Pinned map[int]bool
}

// Tabucol (Hertz & de Werra) minimizes monochromatic edges for a fixed k.
// init is clamped into [0,k); vertices with out-of-range colors get random ones.
// Returns the best coloring found and its conflict count (0 = valid k-coloring).
func Tabucol(g *Graph, init []int, o TabuOpts) ([]int, int) {
	n, k := g.N, o.K
	rng := o.Rng
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	col := make([]int, n)
	for v := 0; v < n; v++ {
		if init != nil && init[v] >= 0 && init[v] < k {
			col[v] = init[v]
		} else {
			col[v] = rng.Intn(k)
		}
	}
	gamma := make([]int32, n*k)
	for u := 0; u < n; u++ {
		for _, v := range g.Adj[u] {
			gamma[u*k+col[v]]++
		}
	}
	conflicts := 0
	for u := 0; u < n; u++ {
		conflicts += int(gamma[u*k+col[u]])
	}
	conflicts /= 2

	// Conflicting-vertex set with O(1) add/remove.
	pos := make([]int, n)
	var cv []int
	for i := range pos {
		pos[i] = -1
	}
	add := func(v int) {
		if pos[v] < 0 {
			pos[v] = len(cv)
			cv = append(cv, v)
		}
	}
	del := func(v int) {
		if p := pos[v]; p >= 0 {
			last := cv[len(cv)-1]
			cv[p] = last
			pos[last] = p
			cv = cv[:len(cv)-1]
			pos[v] = -1
		}
	}
	for v := 0; v < n; v++ {
		if gamma[v*k+col[v]] > 0 {
			add(v)
		}
	}

	tabu := make([]int, n*k) // iteration until which (v,c) is tabu
	best := append([]int(nil), col...)
	bestConf := conflicts

	for it := 0; it < o.MaxIters && conflicts > 0; it++ {
		if o.Stop != nil && it&1023 == 0 && o.Stop() {
			break
		}
		bv, bc, bd := -1, -1, 1<<30
		ties := 0
		for _, v := range cv {
			if o.Pinned[v] {
				continue
			}
			cur := gamma[v*k+col[v]]
			for c := 0; c < k; c++ {
				if c == col[v] {
					continue
				}
				d := int(gamma[v*k+c] - cur)
				if tabu[v*k+c] > it && conflicts+d >= bestConf {
					continue // tabu, and no aspiration
				}
				if d < bd {
					bv, bc, bd, ties = v, c, d, 1
				} else if d == bd {
					ties++
					if rng.Intn(ties) == 0 {
						bv, bc = v, c
					}
				}
			}
		}
		if bv < 0 {
			// Everything tabu: random perturbation of a conflicting vertex.
			if len(cv) == 0 {
				break
			}
			bv = cv[rng.Intn(len(cv))]
			bc = rng.Intn(k)
			if bc == col[bv] {
				continue
			}
			bd = int(gamma[bv*k+bc] - gamma[bv*k+col[bv]])
		}
		old := col[bv]
		col[bv] = bc
		conflicts += bd
		tabu[bv*k+old] = it + int(0.6*float64(len(cv))) + rng.Intn(10)
		for _, w := range g.Adj[bv] {
			gamma[w*k+old]--
			gamma[w*k+bc]++
			if gamma[w*k+col[w]] > 0 {
				add(w)
			} else {
				del(w)
			}
		}
		if gamma[bv*k+bc] > 0 {
			add(bv)
		} else {
			del(bv)
		}
		if conflicts < bestConf {
			bestConf = conflicts
			copy(best, col)
		}
	}
	return best, bestConf
}

// DropColor removes color class c from a coloring (re-labeling so the result
// uses colors 0..k-2) and assigns the freed vertices the least-conflicting color.
// This is how an agent turns the shared best k-coloring into a (k-1) start.
func DropColor(g *Graph, col []int, c int, k int, rng *rand.Rand) []int {
	out := make([]int, len(col))
	var freed []int
	for v, x := range col {
		switch {
		case x == c:
			out[v] = -1
			freed = append(freed, v)
		case x > c:
			out[v] = x - 1
		default:
			out[v] = x
		}
	}
	nk := k - 1
	rng.Shuffle(len(freed), func(i, j int) { freed[i], freed[j] = freed[j], freed[i] })
	cnt := make([]int, nk)
	for _, v := range freed {
		for i := range cnt {
			cnt[i] = 0
		}
		for _, w := range g.Adj[v] {
			if out[w] >= 0 {
				cnt[out[w]]++
			}
		}
		bc := 0
		for i := 1; i < nk; i++ {
			if cnt[i] < cnt[bc] || (cnt[i] == cnt[bc] && rng.Intn(2) == 0) {
				bc = i
			}
		}
		out[v] = bc
	}
	return out
}

// ClassSizes returns the size of each color class.
func ClassSizes(col []int, k int) []int {
	s := make([]int, k)
	for _, c := range col {
		if c >= 0 && c < k {
			s[c]++
		}
	}
	return s
}
