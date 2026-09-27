package verify

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/proto"
	"github.com/jakeroggenbuck/swarm/internal/smt"
)

// mycielski builds M_k: M_2 = K2, chi(M_k) = k, triangle-free for k >= 3.
func mycielski(k int) *graph.Graph {
	n := 2
	edges := [][2]int{{0, 1}}
	for i := 2; i < k; i++ {
		var next [][2]int
		next = append(next, edges...)
		for _, e := range edges {
			next = append(next, [2]int{e[0], n + e[1]}, [2]int{n + e[0], e[1]})
		}
		for v := 0; v < n; v++ {
			next = append(next, [2]int{n + v, 2 * n})
		}
		edges = next
		n = 2*n + 1
	}
	return graph.New("myciel", n, edges)
}

func all(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

func checkers(t *testing.T, g *graph.Graph) []*Checker {
	cs := []*Checker{{G: g, Timeout: 10 * time.Second, MaxS: 200}}
	if z := smt.Find(); z != "" {
		cs = append(cs, &Checker{G: g, Z3: z, Timeout: 10 * time.Second, MaxS: 200})
	} else {
		t.Log("z3 not installed; testing native solver only")
	}
	return cs
}

func TestSubgraphBound(t *testing.T) {
	g := mycielski(4) // Grötzsch graph: 11 vertices, triangle-free, chi = 4
	if g.N != 11 {
		t.Fatalf("grötzsch has %d vertices", g.N)
	}
	for _, c := range checkers(t, g) {
		v := c.Check(context.Background(), Job{Kind: proto.KindSubgraphBound, Claim: proto.Claim{K: 4, S: all(11)}})
		if v.Status != proto.StatusVerified {
			t.Errorf("%s: chi>=4 should verify, got %s (%s)", c.Engine(), v.Status, v.Reason)
		}
		v = c.Check(context.Background(), Job{Kind: proto.KindSubgraphBound, Claim: proto.Claim{K: 5, S: all(11)}})
		if v.Status != proto.StatusRefuted || len(v.Counter) == 0 {
			t.Errorf("%s: chi>=5 should be refuted with a counterexample, got %s", c.Engine(), v.Status)
		}
		// The counterexample must be a proper coloring of what it covers.
		col := map[int]int{}
		for _, p := range v.Counter {
			col[p[0]] = p[1]
		}
		for _, e := range g.Edges() {
			a, okA := col[e[0]]
			b, okB := col[e[1]]
			if okA && okB && a == b {
				t.Errorf("%s: counterexample is not proper on edge %v", c.Engine(), e)
			}
		}
	}
}

func TestSeparation(t *testing.T) {
	// In a 5-cycle plus chord-free structure, check separation logic on K4 minus an edge:
	// vertices 0,1,2,3 with all edges except 0-3. With k=2, c(0)=c(3) forces a triangle 0/3,1,2 -> not 2-colorable.
	g := graph.New("k4e", 4, [][2]int{{0, 1}, {0, 2}, {1, 2}, {1, 3}, {2, 3}})
	for _, c := range checkers(t, g) {
		v := c.Check(context.Background(), Job{Kind: proto.KindSeparation, Claim: proto.Claim{K: 2, U: proto.Int(0), V: proto.Int(3), S: all(4)}})
		if v.Status != proto.StatusVerified {
			t.Errorf("%s: separation at k=2 should verify: %s %s", c.Engine(), v.Status, v.Reason)
		}
		// With 3 colors, c(0)=c(3) is fine (0,3 share color, 1,2 get the others).
		v = c.Check(context.Background(), Job{Kind: proto.KindSeparation, Claim: proto.Claim{K: 3, U: proto.Int(0), V: proto.Int(3), S: all(4)}})
		if v.Status != proto.StatusRefuted {
			t.Errorf("%s: separation at k=3 should be refuted: %s", c.Engine(), v.Status)
		}
		same := -1
		for _, p := range v.Counter {
			if p[0] == 0 || p[0] == 3 {
				if same >= 0 && same != p[1] {
					t.Errorf("%s: merged vertices got different colors", c.Engine())
				}
				same = p[1]
			}
		}
	}
}

func TestSeparationEdgesStrengthenBounds(t *testing.T) {
	// C5 is 3-chromatic. Pretend a verified separation says c(0) != c(2) at level 2:
	// then {0,1,2} with the extra edge is a triangle, so chi >= 3 from 3 vertices.
	g := graph.New("c5", 5, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 0}})
	for _, c := range checkers(t, g) {
		v := c.Check(context.Background(), Job{Kind: proto.KindSubgraphBound, Claim: proto.Claim{K: 3, S: []int{0, 1, 2}}})
		if v.Status != proto.StatusRefuted {
			t.Errorf("%s: path is 2-colorable, want refuted, got %s", c.Engine(), v.Status)
		}
		v = c.Check(context.Background(), Job{Kind: proto.KindSubgraphBound, Claim: proto.Claim{K: 3, S: []int{0, 1, 2}}, Extra: [][2]int{{0, 2}}})
		if v.Status != proto.StatusVerified {
			t.Errorf("%s: with separation edge, want verified, got %s %s", c.Engine(), v.Status, v.Reason)
		}
	}
}

func TestCheapCheckers(t *testing.T) {
	g := mycielski(4)
	c := &Checker{G: g, Timeout: time.Second, MaxS: 100}
	col := graph.DSatur(g, rand.New(rand.NewSource(1)))
	cl := proto.Claim{Coloring: col}
	proto.Normalize(proto.KindColoring, &cl)
	if v := c.Check(context.Background(), Job{Kind: proto.KindColoring, Claim: cl}); v.Status != proto.StatusVerified {
		t.Fatalf("dsatur coloring rejected: %s", v.Reason)
	}
	bad := append([]int(nil), cl.Coloring...)
	e := g.Edges()[0]
	bad[e[1]] = bad[e[0]]
	if v := c.Check(context.Background(), Job{Kind: proto.KindColoring, Claim: proto.Claim{K: cl.K, Coloring: bad}}); v.Status != proto.StatusRefuted {
		t.Fatal("monochromatic edge accepted")
	}
	if v := c.Check(context.Background(), Job{Kind: proto.KindClique, Claim: proto.Claim{K: 2, S: []int{e[0], e[1]}}}); v.Status != proto.StatusVerified {
		t.Fatal("edge clique rejected")
	}
}

func TestClaimIDStable(t *testing.T) {
	a := proto.Claim{K: 3, U: proto.Int(9), V: proto.Int(2), S: []int{5, 2, 9, 5}}
	b := proto.Claim{K: 3, U: proto.Int(2), V: proto.Int(9), S: []int{9, 5}}
	proto.Normalize(proto.KindSeparation, &a)
	proto.Normalize(proto.KindSeparation, &b)
	if proto.ClaimID(proto.KindSeparation, &a) != proto.ClaimID(proto.KindSeparation, &b) {
		t.Fatal("equivalent separations hash differently")
	}
	c1 := proto.Claim{Coloring: []int{3, 3, 1, 0}}
	c2 := proto.Claim{Coloring: []int{0, 0, 2, 1}}
	proto.Normalize(proto.KindColoring, &c1)
	proto.Normalize(proto.KindColoring, &c2)
	if proto.ClaimID(proto.KindColoring, &c1) != proto.ClaimID(proto.KindColoring, &c2) {
		t.Fatal("relabeled colorings hash differently")
	}
}

func TestTabucolDSJC(t *testing.T) {
	g, err := graph.LoadDIMACS("../../instances/dsjc125.5.col")
	if err != nil {
		t.Skip(err)
	}
	rng := rand.New(rand.NewSource(7))
	col, conf := graph.Tabucol(g, nil, graph.TabuOpts{K: 19, MaxIters: 200000, Rng: rng})
	if conf != 0 {
		t.Fatalf("tabucol failed to 19-color dsjc125.5 (%d conflicts)", conf)
	}
	if _, err := g.CheckColoring(col, 19); err != nil {
		t.Fatal(err)
	}
	q, _ := graph.MaxClique(g, time.Now().Add(2*time.Second), rng)
	if err := g.CheckClique(q); err != nil || len(q) < 9 {
		t.Fatalf("clique %v err %v", q, err)
	}
	t.Logf("19-coloring ok, clique size %d", len(q))
}
