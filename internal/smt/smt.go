// Package smt generates SMT-LIB for "is this small graph k-colorable?" and
// runs it through `z3 -in`.
package smt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jakeroggenbuck/swarm/internal/graph"
)

// Formula is a k-coloring query over a local graph.
type Formula struct {
	Text string
	Hash string // hash of the normalized problem (n, k, sorted edges); independent of symmetry breaking
	N, K int
}

// KColoring encodes "g has a proper k-coloring" with Boolean x_v_c:
//
//	every vertex gets at least one color:  (or x_v_0 ... x_v_{k-1})
//	adjacent vertices never share one:     (or (not x_u_c) (not x_v_c))
//
// A clique found in g is pinned to colors 0..q-1 to break color symmetry;
// this is what keeps unsat proofs fast.
func KColoring(g *graph.Graph, k int) Formula {
	h := sha256.New()
	fmt.Fprintf(h, "n=%d k=%d\n", g.N, k)
	for _, e := range g.Edges() {
		fmt.Fprintf(h, "%d %d\n", e[0], e[1])
	}
	hash := hex.EncodeToString(h.Sum(nil)[:16])

	var b strings.Builder
	b.Grow(64 + g.N*k*24 + g.M*k*40)
	b.WriteString("(set-option :produce-models true)\n(set-logic QF_UF)\n")
	for v := 0; v < g.N; v++ {
		for c := 0; c < k; c++ {
			fmt.Fprintf(&b, "(declare-const x_%d_%d Bool)\n", v, c)
		}
	}
	for v := 0; v < g.N; v++ {
		b.WriteString("(assert (or")
		for c := 0; c < k; c++ {
			fmt.Fprintf(&b, " x_%d_%d", v, c)
		}
		b.WriteString("))\n")
	}
	for _, e := range g.Edges() {
		for c := 0; c < k; c++ {
			fmt.Fprintf(&b, "(assert (or (not x_%d_%d) (not x_%d_%d)))\n", e[0], c, e[1], c)
		}
	}
	if g.N > 0 {
		// Deterministic clique so identical problems produce identical text.
		q := graph.GreedyClique(g, nil, rand.New(rand.NewSource(1)))
		for i, v := range q {
			if i >= k {
				break
			}
			fmt.Fprintf(&b, "(assert x_%d_%d)\n", v, i)
		}
		if len(q) > k {
			// Clique larger than k: trivially unsat; the pins above already force it.
			b.WriteString("(assert false)\n")
		}
	}
	b.WriteString("(check-sat)\n(get-model)\n")
	return Formula{Text: b.String(), Hash: hash, N: g.N, K: k}
}

// Result of a solver run.
type Result struct {
	Status graph.Status
	Model  []int // local vertex -> color when sat
	Raw    string
}

var modelRe = regexp.MustCompile(`\(define-fun x_(\d+)_(\d+) \(\) Bool\s+true\)`)

// RunZ3 pipes the formula to `z3 -in -T:<secs>`.
func RunZ3(ctx context.Context, z3 string, f Formula, timeout time.Duration) (Result, error) {
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	ctx, cancel := context.WithTimeout(ctx, timeout+3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, z3, "-in", "-T:"+strconv.Itoa(secs))
	cmd.Stdin = strings.NewReader(f.Text)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	raw := out.String()
	first := strings.TrimSpace(strings.SplitN(raw, "\n", 2)[0])
	switch first {
	case "unsat":
		return Result{Status: graph.Unsat, Raw: first}, nil
	case "sat":
		model := make([]int, f.N)
		for i := range model {
			model[i] = -1
		}
		for _, m := range modelRe.FindAllStringSubmatch(raw, -1) {
			v, _ := strconv.Atoi(m[1])
			c, _ := strconv.Atoi(m[2])
			if v < f.N && model[v] < 0 {
				model[v] = c
			}
		}
		return Result{Status: graph.Sat, Model: model, Raw: first}, nil
	case "unknown", "timeout":
		return Result{Status: graph.Unknown, Raw: first}, nil
	}
	if err != nil {
		return Result{Status: graph.Unknown, Raw: raw}, fmt.Errorf("z3: %v: %s", err, trim(raw, 200))
	}
	return Result{Status: graph.Unknown, Raw: raw}, fmt.Errorf("z3: unexpected output %q", trim(raw, 200))
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// Find returns the z3 binary path or "" if it is not installed.
func Find() string {
	p, err := exec.LookPath("z3")
	if err != nil {
		return ""
	}
	return p
}
