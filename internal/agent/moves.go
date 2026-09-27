package agent

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/proto"
)

// Plan is what the planner (Claude or the heuristic policy) decides each turn.
// Every field except Move is optional; the executor fills sensible defaults.
type Plan struct {
	Move      string `json:"move"`                    // tabucol | bound | separate | clique | merge | dsatur
	K         int    `json:"k,omitempty"`             // target level (see system prompt)
	Start     string `json:"start,omitempty"`         // tabucol: best | dsatur | random
	DropClass *int   `json:"drop_class,omitempty"`    // tabucol: color class of the best coloring to remove
	Seeds     []int  `json:"seed_vertices,omitempty"` // bound/clique/separate: where to start
	Pair      []int  `json:"pair,omitempty"`          // separate/merge: [u, v]
	MaxSize   int    `json:"max_size,omitempty"`      // bound/separate: certificate size cap
	Note      string `json:"note,omitempty"`          // <=200 char rationale attached to the claim
	Broadcast string `json:"broadcast,omitempty"`     // strategist advice published as a NOTE lead
}

func (p Plan) Label() string {
	s := p.Move
	if p.K > 0 {
		s += fmt.Sprintf(" k=%d", p.K)
	}
	if len(p.Pair) == 2 {
		s += fmt.Sprintf(" pair=(%d,%d)", p.Pair[0], p.Pair[1])
	}
	if p.DropClass != nil {
		s += fmt.Sprintf(" drop=%d", *p.DropClass)
	}
	return s
}

// HeuristicPlan is the no-LLM policy: a weighted random move with random
// parameters. Diversity comes from each agent's own seed.
func HeuristicPlan(rng *rand.Rand, v view, g *graph.Graph) Plan {
	if v.Upper >= g.N || len(v.Best) != g.N {
		return Plan{Move: "dsatur"}
	}
	if v.Lower <= 2 && rng.Intn(2) == 0 {
		return Plan{Move: "clique"}
	}
	r := rng.Float64()
	switch {
	case r < 0.40:
		starts := []string{"best", "best", "best", "dsatur", "random"}
		return Plan{Move: "tabucol", Start: starts[rng.Intn(len(starts))]}
	case r < 0.70:
		return Plan{Move: "separate"}
	case r < 0.92:
		return Plan{Move: "bound"}
	case r < 0.96:
		return Plan{Move: "clique"}
	default:
		return Plan{Move: "merge"}
	}
}

func (a *Agent) execute(ctx context.Context, p Plan, v view) string {
	if v.Upper <= v.Lower {
		select { // solved: idle politely
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
		}
		return "problem closed: chi = " + fmt.Sprint(v.Upper)
	}
	deadline := time.Now().Add(a.cfg.Budget)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	switch p.Move {
	case "dsatur":
		return a.moveDSatur(ctx, v)
	case "tabucol":
		return a.moveTabucol(ctx, p, v, deadline)
	case "bound":
		return a.moveBound(ctx, p, v, deadline)
	case "separate":
		return a.moveSeparate(ctx, p, v, deadline)
	case "clique":
		return a.moveClique(ctx, p, v, deadline)
	case "merge":
		return a.moveMerge(ctx, p, v)
	}
	return "unknown move " + p.Move
}

func (a *Agent) moveDSatur(ctx context.Context, v view) string {
	col := graph.DSatur(a.G, a.rng)
	k := graph.NumColors(col)
	if k < v.Upper {
		a.publish(ctx, proto.KindColoring, proto.Claim{Coloring: col}, nil, fmt.Sprintf("DSatur greedy coloring, %d colors", k), "dsatur")
		return fmt.Sprintf("published DSatur %d-coloring", k)
	}
	return fmt.Sprintf("DSatur gave %d colors (no improvement)", k)
}

// withSeps clones G and adds verified separations valid at level k: every
// k-coloring must respect them, so they prune the search for free.
func (a *Agent) withSeps(v view, k int) (*graph.Graph, []string) {
	edges, ids := sepEdges(v.Seps, k)
	if len(edges) == 0 {
		return a.G, nil
	}
	w := a.G.Clone()
	for _, e := range edges {
		w.AddEdge(e[0], e[1])
	}
	return w, ids
}

func (a *Agent) moveTabucol(ctx context.Context, p Plan, v view, deadline time.Time) string {
	k := p.K
	if k <= 0 || k >= v.Upper {
		k = v.Upper - 1
	}
	if k < v.Lower {
		k = v.Lower
	}
	if k < 1 {
		return "nothing to do"
	}
	w, sepIDs := a.withSeps(v, k)
	var init []int
	var parents []string
	start := p.Start
	if start == "" {
		start = "best"
	}
	switch start {
	case "best":
		if len(v.Best) == a.G.N {
			init = append([]int(nil), v.Best...)
			cur := v.Upper
			if v.UpperID != "" {
				parents = append(parents, v.UpperID)
			}
			for cur > k {
				sizes := graph.ClassSizes(init, cur)
				c := 0
				for i := range sizes {
					if sizes[i] < sizes[c] {
						c = i
					}
				}
				if p.DropClass != nil && *p.DropClass >= 0 && *p.DropClass < cur && cur == v.Upper {
					c = *p.DropClass
				}
				init = graph.DropColor(w, init, c, cur, a.rng)
				cur--
			}
		}
	case "dsatur":
		init = graph.DSatur(w, a.rng)
	}
	col, conf := graph.Tabucol(w, init, graph.TabuOpts{K: k, MaxIters: 1 << 30, Rng: a.rng, Stop: func() bool {
		return time.Now().After(deadline) || ctx.Err() != nil
	}})
	if conf > 0 {
		return fmt.Sprintf("tabucol k=%d from %s: %d conflicts left", k, start, conf)
	}
	if _, err := a.G.CheckColoring(col, k); err != nil {
		return "internal: tabucol produced invalid coloring: " + err.Error()
	}
	parents = append(parents, clipStr(sepIDs, 8)...)
	note := p.Note
	if note == "" {
		note = fmt.Sprintf("tabucol from %s start found a %d-coloring", start, k)
	}
	a.publish(ctx, proto.KindColoring, proto.Claim{Coloring: col}, parents, note, fmt.Sprintf("tabucol k=%d", k))
	return fmt.Sprintf("published %d-coloring", k)
}

func (a *Agent) moveClique(ctx context.Context, p Plan, v view, deadline time.Time) string {
	best := graph.GreedyClique(a.G, validSeeds(a.G, p.Seeds, a.focus), a.rng)
	half := time.Now().Add(time.Until(deadline) / 2)
	if q, _ := graph.MaxClique(a.G, half, a.rng); len(q) > len(best) {
		best = q
	}
	if len(best) > v.Lower {
		a.publish(ctx, proto.KindClique, proto.Claim{S: best}, nil, fmt.Sprintf("clique of size %d", len(best)), "clique")
		return fmt.Sprintf("published %d-clique", len(best))
	}
	return fmt.Sprintf("best clique %d (no improvement)", len(best))
}

func validSeeds(g *graph.Graph, seeds, fallback []int) []int {
	var out []int
	for _, s := range seeds {
		if s >= 0 && s < g.N {
			out = append(out, s)
		}
	}
	if len(out) == 0 && len(fallback) > 0 {
		out = fallback[:1]
	}
	return out
}

// testColorable decides whether G[S] (+extra, +merge) is k-colorable with
// the native solver on its k-core, within a short deadline. When it is, the
// coloring (original vertex -> color) comes back to guide the next step.
func testColorable(g *graph.Graph, S []int, extra [][2]int, merge *[2]int, k int, d time.Duration) (graph.Status, map[int]int) {
	sub, err := graph.Induce(g, S, graph.SubOpts{Extra: extra, Merge: merge})
	if err != nil {
		return graph.Unknown, nil
	}
	core, removed := graph.CoreOrder(sub.G, k)
	full := make([]int, sub.G.N)
	for i := range full {
		full[i] = -1
	}
	if len(core) > 0 {
		lg, keep := graph.InducedLocal(sub.G, core)
		st, col := graph.Colorable(lg, k, time.Now().Add(d))
		if st != graph.Sat {
			return st, nil
		}
		for li, c := range col {
			full[keep[li]] = c
		}
	}
	graph.ExtendColoring(sub.G, full, removed)
	out := make(map[int]int, len(sub.Local))
	for o, l := range sub.Local {
		out[o] = full[l]
	}
	return graph.Sat, out
}

// grow adds vertices to S until G[S] (+extra, +merge) is proven not
// k-colorable. Candidates are scored against the current counterexample
// coloring: a vertex whose neighbors in S already use many distinct colors
// is the one that coloring cannot absorb, so it is the most likely to break it.
func (a *Agent) grow(w *graph.Graph, S []int, extra [][2]int, merge *[2]int, k, maxSize int, deadline time.Time) ([]int, graph.Status) {
	in := graph.NewBitset(a.G.N)
	for _, x := range S {
		in.Set(x)
	}
	if merge != nil {
		in.Set(merge[0])
		in.Set(merge[1])
	}
	last := graph.Unknown
	var col map[int]int
	seen := make([]bool, k+1)
	for len(S) <= maxSize && time.Now().Before(deadline) {
		col = nil
		if len(S) >= k {
			last, col = testColorable(a.G, S, extra, merge, k, 400*time.Millisecond)
			if last != graph.Sat {
				return S, last
			}
		}
		if len(S) == maxSize {
			break
		}
		type cand struct{ v, s int }
		var cs []cand
		for x := 0; x < a.G.N; x++ {
			if in.Has(x) {
				continue
			}
			deg := graph.AndCount(w.Row(x), in)
			score := deg * 8
			if col != nil {
				for i := range seen {
					seen[i] = false
				}
				distinct := 0
				for _, y := range w.Adj[x] {
					if c, ok := col[y]; ok && c >= 0 && c <= k && !seen[c] {
						seen[c] = true
						distinct++
					}
				}
				score = distinct*64 + deg*4
			}
			cs = append(cs, cand{x, score + a.rng.Intn(6)})
		}
		if len(cs) == 0 {
			break
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].s > cs[j].s })
		pick := cs[a.rng.Intn(min(3, len(cs)))].v
		S = append(S, pick)
		in.Set(pick)
	}
	return S, last
}

// minimize drops vertices that are not needed for the certificate, so the
// verifier (and anyone reading it) sees a small witness.
func (a *Agent) minimize(S []int, keep map[int]bool, extra [][2]int, merge *[2]int, k int, deadline time.Time) []int {
	order := append([]int(nil), S...)
	a.rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	cur := append([]int(nil), S...)
	for _, x := range order {
		if time.Now().After(deadline) {
			break
		}
		if keep[x] {
			continue
		}
		trial := make([]int, 0, len(cur))
		for _, y := range cur {
			if y != x {
				trial = append(trial, y)
			}
		}
		if st, _ := testColorable(a.G, trial, extra, merge, k, 150*time.Millisecond); st == graph.Unsat {
			cur = trial
		}
	}
	return cur
}

func usedSeps(v view, S []int, level int) []string {
	in := map[int]bool{}
	for _, x := range S {
		in[x] = true
	}
	var ids []string
	for _, s := range v.Seps {
		if s.K >= level && in[s.U] && in[s.V] {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// moveBound searches for S with G[S] (+ verified separations) not
// (k-1)-colorable, proving chi(G) >= k.
func (a *Agent) moveBound(ctx context.Context, p Plan, v view, deadline time.Time) string {
	k := p.K
	if k <= v.Lower || k > v.Upper {
		k = v.Lower + 1
	}
	level := k - 1
	w, _ := a.withSeps(v, level)
	extra, _ := sepEdges(v.Seps, level)
	maxSize := p.MaxSize
	if maxSize <= 0 || maxSize > 120 {
		maxSize = 30 + a.rng.Intn(40)
	}
	seeds := validSeeds(a.G, p.Seeds, a.focus)
	var parents []string
	if len(p.Seeds) == 0 && len(v.Witness) > 0 && a.rng.Intn(2) == 0 {
		// Grow from the swarm's current best certificate and credit it.
		seeds = v.Witness
		if v.LowerID != "" {
			parents = append(parents, v.LowerID)
		}
	}
	S := graph.GreedyClique(w, seeds, a.rng)
	growDeadline := time.Now().Add(time.Until(deadline) * 2 / 3)
	S, st := a.grow(w, S, extra, nil, level, maxSize, growDeadline)
	switch st {
	case graph.Unsat:
		S = a.minimize(S, nil, extra, nil, level, deadline)
		note := p.Note
		if note == "" {
			note = fmt.Sprintf("dense core of %d vertices is not %d-colorable", len(S), level)
		}
		a.publish(ctx, proto.KindSubgraphBound, proto.Claim{K: k, S: S}, append(parents, usedSeps(v, S, level)...), note, fmt.Sprintf("bound k=%d", k))
		return fmt.Sprintf("published chi>=%d with |S|=%d", k, len(S))
	case graph.Unknown:
		if len(S) >= k && a.rng.Intn(3) == 0 {
			// Too hard for the local solver: hand it to the Z3 harness as a lead.
			a.publish(ctx, proto.KindSubgraphBound, proto.Claim{K: k, S: S}, append(parents, usedSeps(v, S, level)...),
				fmt.Sprintf("speculative: %d-vertex core, local solver timed out", len(S)), fmt.Sprintf("bound k=%d (speculative)", k))
			return fmt.Sprintf("published speculative chi>=%d with |S|=%d for Z3", k, len(S))
		}
		return fmt.Sprintf("bound k=%d: local solver undecided at |S|=%d", k, len(S))
	}
	return fmt.Sprintf("bound k=%d: G[S] still %d-colorable at |S|=%d", k, level, len(S))
}

// moveSeparate looks for a non-adjacent pair u,v such that forcing
// c(u)=c(v) makes a small subgraph not L-colorable. The fast path is a
// clique K of size L with N(u) ∪ N(v) ⊇ K: merging u,v closes an (L+1)-clique.
func (a *Agent) moveSeparate(ctx context.Context, p Plan, v view, deadline time.Time) string {
	L := p.K
	if L < v.Lower || L >= v.Upper {
		L = v.Lower
	}
	w, _ := a.withSeps(v, L)
	extra, _ := sepEdges(v.Seps, L)
	maxSize := p.MaxSize
	if maxSize <= 0 || maxSize > 100 {
		maxSize = 20 + a.rng.Intn(30)
	}

	var u, x int
	if len(p.Pair) == 2 && p.Pair[0] >= 0 && p.Pair[1] >= 0 && p.Pair[0] < a.G.N && p.Pair[1] < a.G.N &&
		p.Pair[0] != p.Pair[1] && !w.HasEdge(p.Pair[0], p.Pair[1]) {
		u, x = p.Pair[0], p.Pair[1]
	} else {
		var ok bool
		u, x, ok = a.pickPair(w, validSeeds(a.G, p.Seeds, a.focus), L)
		if !ok {
			return "separate: no candidate pair"
		}
	}
	merge := &[2]int{u, x}

	// Seed S with a clique inside N(u) ∪ N(v); merging u,v makes it adjacent to all of it.
	union := graph.NewBitset(a.G.N)
	for _, y := range w.Adj[u] {
		union.Set(y)
	}
	for _, y := range w.Adj[x] {
		union.Set(y)
	}
	var cands []int
	for y := 0; y < a.G.N; y++ {
		if union.Has(y) && y != u && y != x {
			cands = append(cands, y)
		}
	}
	S := []int{u, x}
	if len(cands) > 0 {
		sub, _ := graph.Induce(w, cands, graph.SubOpts{})
		q := graph.GreedyClique(sub.G, []int{a.rng.Intn(sub.G.N)}, a.rng)
		for _, lq := range q {
			S = append(S, sub.Orig[lq])
		}
	}
	growDeadline := time.Now().Add(time.Until(deadline) * 2 / 3)
	S, st := a.grow(w, S, extra, merge, L, maxSize, growDeadline)
	if st != graph.Unsat {
		return fmt.Sprintf("separate (%d,%d) k=%d: no certificate at |S|=%d (%s)", u, x, L, len(S), st)
	}
	S = a.minimize(S, map[int]bool{u: true, x: true}, extra, merge, L, deadline)
	note := p.Note
	if note == "" {
		note = fmt.Sprintf("c(%d)=c(%d) forces a non-%d-colorable %d-vertex subgraph", u, x, L, len(S))
	}
	a.publish(ctx, proto.KindSeparation, proto.Claim{K: L, U: proto.Int(u), V: proto.Int(x), S: S}, usedSeps(v, S, L), note,
		fmt.Sprintf("separate (%d,%d) k=%d", u, x, L))
	return fmt.Sprintf("published separation (%d,%d) at k=%d with |S|=%d", u, x, L, len(S))
}

// pickPair chooses a non-adjacent pair near the seeds with many common
// neighbors (in the working graph), randomized among the top few.
func (a *Agent) pickPair(w *graph.Graph, seeds []int, L int) (int, int, bool) {
	type pr struct{ u, v, s int }
	var ps []pr
	us := append([]int(nil), seeds...)
	if len(us) == 0 || a.rng.Intn(2) == 0 {
		for i := 0; i < 6; i++ {
			us = append(us, a.rng.Intn(a.G.N))
		}
	}
	for _, u := range us {
		for x := 0; x < a.G.N; x++ {
			if x == u || w.HasEdge(u, x) {
				continue
			}
			// Union size matters most (it hosts the clique), then common neighbors.
			common := graph.AndCount(w.Row(u), w.Row(x))
			union := w.Degree(u) + w.Degree(x) - common
			ps = append(ps, pr{u, x, union + common/2 + a.rng.Intn(8)})
		}
	}
	if len(ps) == 0 {
		return 0, 0, false
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].s > ps[j].s })
	p := ps[a.rng.Intn(min(8, len(ps)))]
	return p.u, p.v, true
}

// moveMerge publishes a heuristic MERGE lead: two non-adjacent vertices that
// share a color in the best coloring and have many common neighbors.
func (a *Agent) moveMerge(ctx context.Context, p Plan, v view) string {
	if len(v.Best) != a.G.N {
		return "merge: no coloring yet"
	}
	var u, x int
	if len(p.Pair) == 2 && p.Pair[0] >= 0 && p.Pair[1] >= 0 && p.Pair[0] < a.G.N && p.Pair[1] < a.G.N &&
		p.Pair[0] != p.Pair[1] && !a.G.HasEdge(p.Pair[0], p.Pair[1]) {
		u, x = p.Pair[0], p.Pair[1]
	} else {
		bestS := -1
		for t := 0; t < 400; t++ {
			i, j := a.rng.Intn(a.G.N), a.rng.Intn(a.G.N)
			if i == j || v.Best[i] != v.Best[j] {
				continue
			}
			if s := graph.AndCount(a.G.Row(i), a.G.Row(j)); s > bestS {
				bestS, u, x = s, i, j
			}
		}
		if bestS < 0 {
			return "merge: no pair"
		}
	}
	note := p.Note
	if note == "" {
		note = fmt.Sprintf("%d and %d share color %d in the best coloring and %d neighbors", u, x, v.Best[u], graph.AndCount(a.G.Row(u), a.G.Row(x)))
	}
	a.publish(ctx, proto.KindMerge, proto.Claim{K: v.Upper, U: proto.Int(u), V: proto.Int(x)}, []string{v.UpperID}, note, "merge")
	return fmt.Sprintf("published MERGE lead (%d,%d)", u, x)
}

func clipStr(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
