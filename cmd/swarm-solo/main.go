// swarm-solo is the demo baseline: hand the WHOLE graph to Z3 and ask it to
// prove chi(G) >= k (i.e. G is not (k-1)-colorable). On hard instances it
// times out, while the swarm proves the same bound with small certificates.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/smt"
)

func main() {
	var (
		gpath   = flag.String("graph", "instances/dsjc125.5.col", "DIMACS .col instance")
		k       = flag.Int("k", 13, "try to prove chi >= k (G not (k-1)-colorable)")
		timeout = flag.Duration("timeout", 60*time.Second, "z3 timeout")
		native  = flag.Bool("native", false, "use the in-process DSatur branch-and-bound instead of z3")
	)
	flag.Parse()
	g, err := graph.LoadDIMACS(*gpath)
	if err != nil {
		log.Fatal(err)
	}
	level := *k - 1
	core := graph.Core(g, level)
	lg, _ := graph.InducedLocal(g, core)
	f := smt.KColoring(lg, level)
	fmt.Printf("%s: n=%d m=%d. Question: is G %d-colorable? (unsat would prove chi >= %d)\n", g.Name, g.N, g.M, level, *k)
	fmt.Printf("formula: %d vertices after %d-core pruning, %d Boolean vars, %d KB of SMT-LIB\n",
		lg.N, level, lg.N*level, len(f.Text)/1024)

	start := time.Now()
	var st graph.Status
	if *native {
		st, _ = graph.Colorable(lg, level, time.Now().Add(*timeout))
		fmt.Printf("native solver: %s after %s\n", st, time.Since(start).Round(time.Millisecond))
	} else {
		z3 := smt.Find()
		if z3 == "" {
			log.Fatal("z3 not found in PATH (brew install z3), or pass -native")
		}
		r, err := smt.RunZ3(context.Background(), z3, f, *timeout)
		if err != nil {
			fmt.Println("z3 error:", err)
		}
		st = r.Status
		fmt.Printf("z3: %s after %s\n", st, time.Since(start).Round(time.Millisecond))
	}
	switch st {
	case graph.Unsat:
		fmt.Printf("=> proved chi >= %d on the full graph\n", *k)
	case graph.Sat:
		fmt.Printf("=> G is %d-colorable, so chi <= %d\n", level, level)
	default:
		fmt.Printf("=> no answer within %s. The monolithic query is too hard.\n", *timeout)
	}
}
