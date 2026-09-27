// swarm-server: the realtime verified log for an agent swarm.
package main

import (
	"flag"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/server"
	"github.com/jakeroggenbuck/swarm/internal/smt"
	"github.com/jakeroggenbuck/swarm/internal/state"
	"github.com/jakeroggenbuck/swarm/web"
)

// Best known results from the literature (chromatic number where settled).
var known = map[string][2]int{ // name -> {known lower, known upper}; 0 = not claimed here
	"dsjc125.1": {5, 5},
	"dsjc125.5": {0, 17},
	"dsjc125.9": {0, 44},
	"dsjc250.5": {0, 28},
	"dsjc500.5": {0, 47},
	"le450_15a": {15, 15},
	"le450_15c": {15, 15},
	"le450_25c": {25, 25},
	"queen8_8":  {9, 9},
	"queen9_9":  {10, 10},
	"myciel5":   {6, 6},
	"myciel6":   {7, 7},
	"myciel7":   {8, 8},
}

func main() {
	var (
		addr    = flag.String("addr", ":8080", "listen address")
		gpath   = flag.String("graph", "instances/dsjc125.5.col", "DIMACS .col instance")
		workers = flag.Int("workers", runtime.NumCPU(), "verifier workers")
		timeout = flag.Duration("timeout", 10*time.Second, "per-claim solver timeout (z3 -T)")
		z3flag  = flag.String("z3", "auto", "z3 binary path, 'auto' to search PATH, 'none' for the native solver")
		logPath = flag.String("log", "", "append-only event log for replay after restart (optional)")
		ring    = flag.Int("ring", 1<<16, "ring size (power of two)")
		maxS    = flag.Int("max-s", 160, "largest certificate subgraph accepted")
	)
	flag.Parse()

	g, err := graph.LoadDIMACS(*gpath)
	if err != nil {
		log.Fatalf("load graph: %v", err)
	}
	z3 := *z3flag
	switch z3 {
	case "auto":
		z3 = smt.Find()
	case "none":
		z3 = ""
	}
	engine := "z3 (" + z3 + ")"
	if z3 == "" {
		engine = "native DSatur branch-and-bound (z3 not found)"
	}

	p := state.Problem{Name: g.Name, N: g.N, M: g.M, Density: g.Density()}
	if k, ok := known[strings.ToLower(g.Name)]; ok {
		p.KnownLower, p.KnownUpper = k[0], k[1]
	}
	s, err := server.New(g, p, server.Config{
		RingSize: *ring, Workers: *workers, Z3: z3, SolverTimeout: *timeout,
		LogPath: *logPath, MaxS: *maxS,
	})
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	s.Start()

	log.Printf("swarm: %s  n=%d m=%d density=%.2f", g.Name, g.N, g.M, g.Density())
	log.Printf("verifier: %d workers, %s, timeout %s", *workers, engine, *timeout)
	log.Printf("dashboard http://localhost%s   ws ws://localhost%s/ws", *addr, *addr)
	srv := &http.Server{Addr: *addr, Handler: s.Handler(web.Handler()), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
