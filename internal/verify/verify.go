// Package verify checks claim certificates. COLORING and CLIQUE are checked
// in-process; SUBGRAPH_BOUND and SEPARATION reduce to a small k-coloring
// query over G[S] that goes to Z3 (or the native solver as a fallback).
package verify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/proto"
	"github.com/jakeroggenbuck/swarm/internal/smt"
)

// Job is one claim to check. Extra holds edges implied by verified
// separations the claim cites as parents.
type Job struct {
	ID       string
	Kind     string
	Agent    string
	Claim    proto.Claim
	Extra    [][2]int
	Enqueued time.Time
}

// Checker is safe for concurrent use.
type Checker struct {
	G       *graph.Graph
	Z3      string        // z3 binary; "" = use the native solver
	Timeout time.Duration // per solver call
	MaxS    int           // largest certificate subgraph accepted

	cache sync.Map // formula hash -> cached

	Z3Calls     atomic.Int64
	NativeCalls atomic.Int64
	CacheHits   atomic.Int64
}

type cached struct {
	status graph.Status
	model  []int
}

// Engine names the solver used for subgraph checks.
func (c *Checker) Engine() string {
	if c.Z3 != "" {
		return "z3"
	}
	return "native"
}

// Check runs the certificate check for one job.
func (c *Checker) Check(ctx context.Context, j Job) (v proto.Verdict) {
	start := time.Now()
	v = proto.Verdict{Of: j.ID, Kind: j.Kind, K: j.Claim.K, Agent: j.Agent}
	defer func() { v.Ms = float64(time.Since(start).Microseconds()) / 1000 }()

	cl := j.Claim
	switch j.Kind {
	case proto.KindColoring:
		v.Engine = "edge-scan"
		used, err := c.G.CheckColoring(cl.Coloring, cl.K)
		if err != nil {
			v.Status, v.Reason = proto.StatusRefuted, err.Error()
		} else {
			v.Status = proto.StatusVerified
			v.Reason = fmt.Sprintf("valid %d-coloring of all %d vertices", used, c.G.N)
		}
	case proto.KindClique:
		v.Engine = "pair-scan"
		if err := c.G.CheckClique(cl.S); err != nil {
			v.Status, v.Reason = proto.StatusRefuted, err.Error()
		} else {
			v.Status = proto.StatusVerified
			v.Reason = fmt.Sprintf("%d vertices pairwise adjacent", len(cl.S))
		}
	case proto.KindSubgraphBound:
		// chi(G) >= k  <=  G[S] (+ separation edges) is not (k-1)-colorable.
		if len(cl.S) > c.MaxS {
			return reject(v, proto.StatusSkipped, fmt.Sprintf("certificate has %d vertices (max %d)", len(cl.S), c.MaxS))
		}
		sub, err := graph.Induce(c.G, cl.S, graph.SubOpts{Extra: j.Extra})
		if err != nil {
			return reject(v, proto.StatusRefuted, err.Error())
		}
		c.solveInto(ctx, &v, sub, cl.K-1)
		if v.Status == proto.StatusVerified {
			v.Reason = fmt.Sprintf("G[S] with |S|=%d is not %d-colorable (unsat on %d-vertex core)", len(cl.S), cl.K-1, v.Core)
		}
	case proto.KindSeparation:
		// every k-coloring has c(u) != c(v)  <=  G[S] with u,v contracted is not k-colorable.
		if len(cl.S) > c.MaxS {
			return reject(v, proto.StatusSkipped, fmt.Sprintf("certificate has %d vertices (max %d)", len(cl.S), c.MaxS))
		}
		m := [2]int{*cl.U, *cl.V}
		sub, err := graph.Induce(c.G, cl.S, graph.SubOpts{Extra: j.Extra, Merge: &m})
		if errors.Is(err, graph.ErrTrivialMerge) {
			return reject(v, proto.StatusSkipped, "u and v are already adjacent")
		}
		if err != nil {
			return reject(v, proto.StatusRefuted, err.Error())
		}
		c.solveInto(ctx, &v, sub, cl.K)
		if v.Status == proto.StatusVerified {
			v.Reason = fmt.Sprintf("G[S]/(u=v) is not %d-colorable, so c(%d) != c(%d) in every %d-coloring", cl.K, *cl.U, *cl.V, cl.K)
		}
	default:
		return reject(v, proto.StatusSkipped, "kind is not verifiable")
	}
	return v
}

func reject(v proto.Verdict, status, reason string) proto.Verdict {
	v.Status, v.Reason = status, reason
	return v
}

// solveInto decides k-colorability of sub, pruned to its k-core, and fills v.
// unsat => verified; sat => refuted with a counterexample coloring.
func (c *Checker) solveInto(ctx context.Context, v *proto.Verdict, sub *graph.Sub, k int) {
	core, removed := graph.CoreOrder(sub.G, k)
	v.Core = len(core)
	full := make([]int, sub.G.N) // local coloring for counterexamples
	for i := range full {
		full[i] = -1
	}
	status := graph.Sat
	if len(core) == 0 {
		v.Engine = "k-core"
	} else {
		lg, keep := graph.InducedLocal(sub.G, core)
		st, model, engine, err := c.Solve(ctx, lg, k)
		v.Engine, status = engine, st
		if st == graph.Unknown {
			v.Status = proto.StatusUnknown
			v.Reason = fmt.Sprintf("solver gave up after %s on %d-vertex core", c.Timeout, len(core))
			if err != nil {
				v.Reason = err.Error()
			}
			return
		}
		for li, col := range model {
			full[keep[li]] = col
		}
	}
	if status == graph.Unsat {
		v.Status = proto.StatusVerified
		return
	}
	// Sat: the core coloring extends to all of G[S]; report it as the counterexample.
	graph.ExtendColoring(sub.G, full, removed)
	v.Status = proto.StatusRefuted
	if len(core) == 0 {
		v.Reason = fmt.Sprintf("G[S] peels to an empty %d-core, so it is %d-colorable", k, k)
	} else {
		v.Reason = fmt.Sprintf("found a %d-coloring (core %d of %d vertices)", k, len(core), sub.G.N)
	}
	for sl, col := range full {
		if col < 0 {
			continue
		}
		ov := sub.Orig[sl]
		v.Counter = append(v.Counter, [2]int{ov, col})
		if sl == sub.Merged {
			// Both merged endpoints share this color.
			for o, l := range sub.Local {
				if l == sl && o != ov {
					v.Counter = append(v.Counter, [2]int{o, col})
				}
			}
		}
	}
}

// Solve decides k-colorability of a local graph with caching.
func (c *Checker) Solve(ctx context.Context, g *graph.Graph, k int) (graph.Status, []int, string, error) {
	f := smt.KColoring(g, k)
	if hit, ok := c.cache.Load(f.Hash); ok {
		c.CacheHits.Add(1)
		h := hit.(cached)
		return h.status, h.model, "cache", nil
	}
	var (
		st    graph.Status
		model []int
		err   error
		eng   string
	)
	if c.Z3 != "" {
		c.Z3Calls.Add(1)
		var r smt.Result
		r, err = smt.RunZ3(ctx, c.Z3, f, c.Timeout)
		st, model, eng = r.Status, r.Model, "z3"
	} else {
		c.NativeCalls.Add(1)
		st, model = graph.Colorable(g, k, time.Now().Add(c.Timeout))
		eng = "native"
	}
	if st != graph.Unknown {
		c.cache.Store(f.Hash, cached{st, model})
	}
	return st, model, eng, err
}

// Pool runs checks on a fixed set of workers. Cheap checks (COLORING, CLIQUE)
// get their own lane so they never wait behind a 10 second Z3 call.
type Pool struct {
	C    *Checker
	slow chan Job
	fast chan Job
	// Skip is consulted just before a job runs so claims made stale while
	// queued (a better bound already landed) don't burn solver time.
	Skip  func(Job) (bool, string)
	Done  func(Job, proto.Verdict)
	Depth atomic.Int64
	Busy  atomic.Int64
}

func NewPool(c *Checker, workers, queue int, done func(Job, proto.Verdict)) *Pool {
	p := &Pool{C: c, slow: make(chan Job, queue), fast: make(chan Job, queue), Done: done}
	for i := 0; i < workers; i++ {
		go p.worker(p.slow)
	}
	for i := 0; i < 2; i++ {
		go p.worker(p.fast)
	}
	return p
}

// Submit enqueues without blocking; false means the queue is full.
func (p *Pool) Submit(j Job) bool {
	ch := p.slow
	if j.Kind == proto.KindColoring || j.Kind == proto.KindClique {
		ch = p.fast
	}
	select {
	case ch <- j:
		p.Depth.Add(1)
		return true
	default:
		return false
	}
}

func (p *Pool) worker(ch chan Job) {
	for j := range ch {
		p.Depth.Add(-1)
		if p.Skip != nil {
			if skip, why := p.Skip(j); skip {
				p.Done(j, proto.Verdict{Of: j.ID, Kind: j.Kind, K: j.Claim.K, Agent: j.Agent, Status: proto.StatusSkipped, Reason: why})
				continue
			}
		}
		p.Busy.Add(1)
		v := p.C.Check(context.Background(), j)
		p.Busy.Add(-1)
		p.Done(j, v)
	}
}
