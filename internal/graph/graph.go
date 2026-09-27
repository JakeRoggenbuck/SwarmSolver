// Package graph holds the problem graph, the DIMACS parser, and the cheap
// certificate checkers (COLORING, CLIQUE) that never need a solver.
package graph

import (
	"bufio"
	"fmt"
	"io"
	"math/bits"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Graph is an undirected simple graph on vertices 0..N-1. Adjacency is kept
// both as bitsets (O(1) edge test, fast set intersection) and as lists.
type Graph struct {
	Name string
	N    int
	M    int
	Adj  [][]int
	bits []Bitset
}

// Bitset is a fixed-width set of vertices.
type Bitset []uint64

func NewBitset(n int) Bitset { return make(Bitset, (n+63)/64) }

func (b Bitset) Set(i int)        { b[i>>6] |= 1 << (uint(i) & 63) }
func (b Bitset) Clear(i int)      { b[i>>6] &^= 1 << (uint(i) & 63) }
func (b Bitset) Has(i int) bool   { return b[i>>6]&(1<<(uint(i)&63)) != 0 }
func (b Bitset) Count() int {
	c := 0
	for _, w := range b {
		c += bits.OnesCount64(w)
	}
	return c
}

// AndCount returns |a ∩ b|.
func AndCount(a, b Bitset) int {
	c := 0
	for i := range a {
		c += bits.OnesCount64(a[i] & b[i])
	}
	return c
}

// New builds a graph from an edge list (0-based). Self loops and duplicates are dropped.
func New(name string, n int, edges [][2]int) *Graph {
	g := &Graph{Name: name, N: n, Adj: make([][]int, n), bits: make([]Bitset, n)}
	for i := range g.bits {
		g.bits[i] = NewBitset(n)
	}
	for _, e := range edges {
		g.AddEdge(e[0], e[1])
	}
	for i := range g.Adj {
		sort.Ints(g.Adj[i])
	}
	return g
}

// AddEdge inserts u-v if absent. Returns true if the edge is new.
func (g *Graph) AddEdge(u, v int) bool {
	if u == v || u < 0 || v < 0 || u >= g.N || v >= g.N || g.bits[u].Has(v) {
		return false
	}
	g.bits[u].Set(v)
	g.bits[v].Set(u)
	g.Adj[u] = append(g.Adj[u], v)
	g.Adj[v] = append(g.Adj[v], u)
	g.M++
	return true
}

func (g *Graph) HasEdge(u, v int) bool { return g.bits[u].Has(v) }
func (g *Graph) Row(u int) Bitset       { return g.bits[u] }
func (g *Graph) Degree(u int) int       { return len(g.Adj[u]) }

// Clone deep-copies the graph so callers can add derived edges (verified separations).
func (g *Graph) Clone() *Graph {
	c := &Graph{Name: g.Name, N: g.N, M: g.M, Adj: make([][]int, g.N), bits: make([]Bitset, g.N)}
	for i := 0; i < g.N; i++ {
		c.Adj[i] = append([]int(nil), g.Adj[i]...)
		c.bits[i] = append(Bitset(nil), g.bits[i]...)
	}
	return c
}

// Edges returns every edge once with u < v.
func (g *Graph) Edges() [][2]int {
	out := make([][2]int, 0, g.M)
	for u := 0; u < g.N; u++ {
		for _, v := range g.Adj[u] {
			if u < v {
				out = append(out, [2]int{u, v})
			}
		}
	}
	return out
}

// Density is 2M / N(N-1).
func (g *Graph) Density() float64 {
	if g.N < 2 {
		return 0
	}
	return 2 * float64(g.M) / float64(g.N*(g.N-1))
}

// ParseDIMACS reads the standard .col format ("p edge N M" then "e u v", 1-based).
func ParseDIMACS(name string, r io.Reader) (*Graph, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	n := -1
	var edges [][2]int
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		switch f[0] {
		case "p":
			if len(f) < 4 {
				return nil, fmt.Errorf("bad p line %q", line)
			}
			v, err := strconv.Atoi(f[2])
			if err != nil {
				return nil, fmt.Errorf("bad vertex count: %w", err)
			}
			n = v
		case "e":
			if len(f) < 3 {
				return nil, fmt.Errorf("bad e line %q", line)
			}
			u, err1 := strconv.Atoi(f[1])
			v, err2 := strconv.Atoi(f[2])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("bad e line %q", line)
			}
			edges = append(edges, [2]int{u - 1, v - 1})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n < 0 {
		return nil, fmt.Errorf("no p line")
	}
	return New(name, n, edges), nil
}

func LoadDIMACS(path string) (*Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	name := path
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, ".col")
	return ParseDIMACS(name, f)
}

// DIMACS serializes the graph back to .col text (served on GET /problem).
func (g *Graph) DIMACS() string {
	var b strings.Builder
	fmt.Fprintf(&b, "c %s\np edge %d %d\n", g.Name, g.N, g.M)
	for _, e := range g.Edges() {
		fmt.Fprintf(&b, "e %d %d\n", e[0]+1, e[1]+1)
	}
	return b.String()
}

// CheckColoring validates a full coloring with at most k colors in O(N+E).
// Returns the number of distinct colors used.
func (g *Graph) CheckColoring(col []int, k int) (int, error) {
	if len(col) != g.N {
		return 0, fmt.Errorf("coloring has %d entries, graph has %d vertices", len(col), g.N)
	}
	used := make(map[int]bool)
	for v, c := range col {
		if c < 0 || c >= k {
			return 0, fmt.Errorf("vertex %d has color %d outside [0,%d)", v, c, k)
		}
		used[c] = true
	}
	for u := 0; u < g.N; u++ {
		for _, v := range g.Adj[u] {
			if u < v && col[u] == col[v] {
				return 0, fmt.Errorf("edge %d-%d is monochromatic (color %d)", u, v, col[u])
			}
		}
	}
	return len(used), nil
}

// CheckClique validates that every pair in q is adjacent.
func (g *Graph) CheckClique(q []int) error {
	seen := make(map[int]bool, len(q))
	for _, v := range q {
		if v < 0 || v >= g.N {
			return fmt.Errorf("vertex %d out of range", v)
		}
		if seen[v] {
			return fmt.Errorf("vertex %d repeated", v)
		}
		seen[v] = true
	}
	for i := 0; i < len(q); i++ {
		for j := i + 1; j < len(q); j++ {
			if !g.HasEdge(q[i], q[j]) {
				return fmt.Errorf("vertices %d and %d are not adjacent", q[i], q[j])
			}
		}
	}
	return nil
}

// Conflicts counts monochromatic edges (used by local search and for leads).
func (g *Graph) Conflicts(col []int) int {
	c := 0
	for u := 0; u < g.N; u++ {
		for _, v := range g.Adj[u] {
			if u < v && col[u] == col[v] {
				c++
			}
		}
	}
	return c
}

// NumColors counts distinct colors.
func NumColors(col []int) int {
	seen := map[int]bool{}
	for _, c := range col {
		seen[c] = true
	}
	return len(seen)
}

// Normalize relabels colors to 0..k-1 in order of first appearance.
func Normalize(col []int) []int {
	m := map[int]int{}
	out := make([]int, len(col))
	for i, c := range col {
		if _, ok := m[c]; !ok {
			m[c] = len(m)
		}
		out[i] = m[c]
	}
	return out
}
