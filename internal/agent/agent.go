// Package agent is one member of the swarm. It keeps a local view built
// from the verified stream, picks a move each turn (Claude or a random
// heuristic policy), runs a local Go search seeded by that plan, and
// publishes whatever it finds as a claim with a certificate.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/proto"
	"github.com/jakeroggenbuck/swarm/internal/state"
)

type Config struct {
	Server   string // http://host:port
	Name     string
	Model    string // "" = heuristic-only agent
	Role     string // "worker" or "strategist"
	Seed     int64
	Budget   time.Duration // compute budget per move
	Strategy string        // "" = pick one at random
	Verbose  bool
}

// Strategy prompts give each agent a different bias; diversity comes from
// randomness, not from central assignment.
var Strategies = []string{
	"attack dense cores: grow high-density subgraphs to push the lower bound",
	"recolor the largest color class: squeeze the best coloring down by one color",
	"look for separations near high-degree vertices: pairs with many common neighbors",
	"hunt for big cliques and tight witnesses around the current lower-bound certificate",
	"remove the smallest color class and repair with tabu search",
	"reuse verified separations: build bound certificates on top of other agents' lemmas",
}

type outcome struct {
	Move   string
	Result string
	At     time.Time
}

type refutation struct {
	Agent  string
	Kind   string
	K      int
	Reason string
	Size   int
}

type lead struct {
	Agent string
	Kind  string
	Note  string
	U, V  int
}

// Agent state shared between the stream reader and the move loop.
type Agent struct {
	cfg Config
	G   *graph.Graph
	rng *rand.Rand

	conn atomic.Pointer[websocket.Conn]

	mu        sync.Mutex
	upper     int
	upperID   string
	best      []int
	lower     int
	lowerID   string
	witness   []int
	knownUp   int
	seps      []state.Sep
	sepSeen   map[string]bool
	refutes   []refutation
	leads     []lead
	mine      map[string]string // my claim ids -> move label
	outcomes  []outcome
	verified  int
	improved  int
	published int
	turn      int

	focus      []int
	focusTurns int
	planner    Planner
	planFails  int
}

func New(cfg Config, planner Planner) *Agent {
	if cfg.Budget == 0 {
		cfg.Budget = 4 * time.Second
	}
	a := &Agent{cfg: cfg, rng: rand.New(rand.NewSource(cfg.Seed)), sepSeen: map[string]bool{}, mine: map[string]string{}, planner: planner}
	if a.cfg.Strategy == "" {
		a.cfg.Strategy = Strategies[a.rng.Intn(len(Strategies))]
	}
	return a
}

func (a *Agent) logf(format string, args ...any) {
	if a.cfg.Verbose {
		log.Printf("[%s] "+format, append([]any{a.cfg.Name}, args...)...)
	}
}

// FetchProblem downloads the graph from GET /problem.
func FetchProblem(server string) (*graph.Graph, state.Problem, error) {
	resp, err := http.Get(strings.TrimRight(server, "/") + "/problem")
	if err != nil {
		return nil, state.Problem{}, err
	}
	defer resp.Body.Close()
	var body struct {
		Problem state.Problem `json:"problem"`
		DIMACS  string        `json:"dimacs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, state.Problem{}, err
	}
	g, err := graph.ParseDIMACS(body.Problem.Name, strings.NewReader(body.DIMACS))
	return g, body.Problem, err
}

// Run connects, replays the verified history, and loops until ctx ends.
func (a *Agent) Run(ctx context.Context, g *graph.Graph, p state.Problem) error {
	a.G = g
	a.upper, a.lower, a.knownUp = g.N, 1, p.KnownUpper
	u, _ := url.Parse(strings.TrimRight(a.cfg.Server, "/") + "/ws")
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	q := url.Values{}
	topics := "verified,verdicts,state"
	if a.cfg.Model != "" {
		topics += ",leads" // LLM agents read other agents' leads and notes
	}
	q.Set("topics", topics)
	q.Set("from", "1")
	q.Set("agent", a.cfg.Name)
	model := a.cfg.Model
	if model == "" {
		model = "heuristic"
	}
	q.Set("model", model)
	u.RawQuery = q.Encode()

	for {
		c, _, err := websocket.Dial(ctx, u.String(), nil)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.logf("dial: %v; retrying", err)
			time.Sleep(time.Second)
			continue
		}
		c.SetReadLimit(64 << 20)
		a.conn.Store(c)
		readErr := make(chan error, 1)
		go func() { readErr <- a.readLoop(ctx, c) }()
		loopCtx, cancel := context.WithCancel(ctx)
		go a.loop(loopCtx)
		err = <-readErr
		cancel()
		c.CloseNow()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.logf("stream closed: %v; reconnecting", err)
		time.Sleep(time.Second)
	}
}

func (a *Agent) readLoop(ctx context.Context, c *websocket.Conn) error {
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var e proto.Envelope
			if json.Unmarshal(line, &e) == nil {
				a.apply(&e)
			}
		}
	}
}

// apply folds one event into the local view. Idempotent: replays are fine.
func (a *Agent) apply(e *proto.Envelope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case e.Kind == proto.KindSnapshot:
		var v state.View
		if json.Unmarshal(e.State, &v) != nil {
			return
		}
		if v.Upper < a.upper && len(v.BestColoring) == a.G.N {
			a.upper, a.best, a.upperID = v.Upper, v.BestColoring, v.UpperID
		}
		if v.Lower > a.lower {
			a.lower, a.witness, a.lowerID = v.Lower, v.LowerWitness, v.LowerID
		}
		for _, s := range v.Separations {
			a.addSep(s)
		}
	case e.Topic == proto.TopicVerified && e.Claim != nil:
		c := e.Claim
		switch e.Kind {
		case proto.KindColoring:
			if c.K < a.upper && len(c.Coloring) == a.G.N {
				a.upper, a.best, a.upperID = c.K, c.Coloring, e.ID
			}
		case proto.KindClique, proto.KindSubgraphBound:
			if c.K > a.lower {
				a.lower, a.witness, a.lowerID = c.K, c.S, e.ID
			}
		case proto.KindSeparation:
			a.addSep(state.Sep{ID: e.ID, U: *c.U, V: *c.V, K: c.K, Size: len(c.S), Agent: e.Agent})
		}
	case e.Topic == proto.TopicVerdicts && e.Verdict != nil:
		v := e.Verdict
		if move, ok := a.mine[v.Of]; ok {
			res := v.Status
			if v.Reason != "" {
				res += ": " + v.Reason
			}
			a.outcomes = append(a.outcomes, outcome{Move: move, Result: res, At: time.Now()})
			if len(a.outcomes) > 8 {
				a.outcomes = a.outcomes[1:]
			}
			if v.Status == proto.StatusVerified {
				a.verified++
			}
			delete(a.mine, v.Of)
		}
		if v.Status == proto.StatusRefuted {
			a.refutes = append(a.refutes, refutation{Agent: v.Agent, Kind: v.Kind, K: v.K, Reason: v.Reason, Size: len(v.Counter)})
			if len(a.refutes) > 6 {
				a.refutes = a.refutes[1:]
			}
		}
	case e.Topic == proto.TopicLeads && (e.Kind == proto.KindMerge || e.Kind == proto.KindNote) && e.Agent != a.cfg.Name:
		l := lead{Agent: e.Agent, Kind: e.Kind, Note: e.Note}
		if e.Claim != nil && e.Claim.U != nil {
			l.U, l.V = *e.Claim.U, *e.Claim.V
		}
		a.leads = append(a.leads, l)
		if len(a.leads) > 6 {
			a.leads = a.leads[1:]
		}
	}
}

func (a *Agent) addSep(s state.Sep) {
	if !a.sepSeen[s.ID] {
		a.sepSeen[s.ID] = true
		a.seps = append(a.seps, s)
	}
}

// view is a consistent copy of the shared state for one turn.
type view struct {
	Upper, Lower int
	UpperID      string
	LowerID      string
	Best         []int
	Witness      []int
	Seps         []state.Sep
	KnownUp      int
}

func (a *Agent) snapshot() view {
	a.mu.Lock()
	defer a.mu.Unlock()
	return view{Upper: a.upper, Lower: a.lower, UpperID: a.upperID, LowerID: a.lowerID, Best: a.best,
		Witness: a.witness, Seps: append([]state.Sep(nil), a.seps...), KnownUp: a.knownUp}
}

// sepEdges returns verified separation edges usable at coloring level k
// (a separation proven for k holds for every k' <= k), with their ids.
func sepEdges(seps []state.Sep, k int) ([][2]int, []string) {
	var e [][2]int
	var ids []string
	for _, s := range seps {
		if s.K >= k {
			e = append(e, [2]int{s.U, s.V})
			ids = append(ids, s.ID)
		}
	}
	return e, ids
}

// publish sends a claim. The server stamps seq/ts/id; we track the id locally
// by computing the same content address.
func (a *Agent) publish(ctx context.Context, kind string, c proto.Claim, parents []string, note, move string) {
	cc := c
	if err := proto.Normalize(kind, &cc); err != nil {
		a.logf("not publishing malformed %s: %v", kind, err)
		return
	}
	e := proto.Envelope{Kind: kind, Claim: &cc, Parents: parents, Note: note, SentTS: time.Now().UnixMilli()}
	if kind != proto.KindNote {
		id := proto.ClaimID(kind, &cc)
		a.mu.Lock()
		a.mine[id] = move
		a.published++
		a.mu.Unlock()
	}
	data, _ := json.Marshal(e)
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.conn.Load().Write(wctx, websocket.MessageText, data); err != nil {
		a.logf("publish: %v", err)
	}
}

func (a *Agent) loop(ctx context.Context) {
	// Give the replay a moment to land so the first turn sees the current bounds.
	select {
	case <-time.After(300 * time.Millisecond):
	case <-ctx.Done():
		return
	}
	for ctx.Err() == nil {
		a.turn++
		v := a.snapshot()
		a.refocus(v)
		d := a.digest(v)
		plan, err := a.plan(ctx, d, v)
		if err != nil {
			a.planFails++
			msg := err.Error()
			if i := strings.IndexByte(msg, '\n'); i > 0 {
				msg = msg[:i]
			}
			log.Printf("[%s] planner: %s (heuristic move this turn)", a.cfg.Name, msg)
			if a.planFails >= 3 {
				log.Printf("[%s] planner failed 3 times in a row; continuing as a heuristic agent", a.cfg.Name)
				a.planner = nil
			}
			plan = HeuristicPlan(a.rng, v, a.G)
		} else if a.planner != nil {
			a.planFails = 0
		}
		if plan.Broadcast != "" {
			a.publish(ctx, proto.KindNote, proto.Claim{}, nil, plan.Broadcast, "note")
		}
		res := a.execute(ctx, plan, v)
		a.logf("turn %d: %s -> %s", a.turn, plan.Label(), res)
		a.mu.Lock()
		a.outcomes = append(a.outcomes, outcome{Move: plan.Label(), Result: res, At: time.Now()})
		if len(a.outcomes) > 8 {
			a.outcomes = a.outcomes[1:]
		}
		a.mu.Unlock()
	}
}

func (a *Agent) plan(ctx context.Context, d string, v view) (Plan, error) {
	if a.planner == nil || v.Upper == a.G.N {
		// No LLM, or nothing known yet (first move is always a quick coloring).
		return HeuristicPlan(a.rng, v, a.G), nil
	}
	pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return a.planner.Plan(pctx, d)
}

// refocus draws a new random focus region every few turns: a vertex and
// its densest neighbors.
func (a *Agent) refocus(v view) {
	if a.focusTurns > 0 && len(a.focus) > 0 {
		a.focusTurns--
		return
	}
	a.focusTurns = 2 + a.rng.Intn(4)
	center := a.rng.Intn(a.G.N)
	if a.rng.Intn(3) == 0 && len(v.Witness) > 0 {
		center = v.Witness[a.rng.Intn(len(v.Witness))]
	}
	nb := append([]int(nil), a.G.Adj[center]...)
	a.rng.Shuffle(len(nb), func(i, j int) { nb[i], nb[j] = nb[j], nb[i] })
	size := 8 + a.rng.Intn(8)
	if len(nb) > size {
		nb = nb[:size]
	}
	a.focus = append([]int{center}, nb...)
}

// digest is the per-turn context handed to the LLM: bounds, focus region,
// relevant lemmas, and recent refutations. Kept short on purpose.
func (a *Agent) digest(v view) string {
	a.mu.Lock()
	outs := append([]outcome(nil), a.outcomes...)
	refs := append([]refutation(nil), a.refutes...)
	leads := append([]lead(nil), a.leads...)
	a.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "Turn %d. You are %s. Strategy: %s.\n", a.turn, a.cfg.Name, a.cfg.Strategy)
	fmt.Fprintf(&b, "Proven bounds: %d <= chi(G) <= %d (gap %d).", v.Lower, v.Upper, v.Upper-v.Lower)
	if v.KnownUp > 0 {
		fmt.Fprintf(&b, " Best known coloring (target): %d.", v.KnownUp)
	}
	b.WriteString("\n")
	if len(v.Best) == a.G.N {
		sizes := graph.ClassSizes(v.Best, v.Upper)
		type cs struct{ c, n int }
		var list []cs
		for c, n := range sizes {
			list = append(list, cs{c, n})
		}
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if list[j].n < list[i].n {
					list[i], list[j] = list[j], list[i]
				}
			}
		}
		b.WriteString("Best coloring class sizes (color:size, smallest first): ")
		for i, x := range list {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%d:%d", x.c, x.n)
		}
		b.WriteString("\n")
	}
	if len(v.Witness) > 0 {
		fmt.Fprintf(&b, "Lower-bound witness (%d vertices): %v\n", len(v.Witness), clip(v.Witness, 40))
	}
	b.WriteString("Your focus region (vertex:degree): ")
	for i, x := range a.focus {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d:%d", x, a.G.Degree(x))
	}
	b.WriteString("\n")
	inFocus := map[int]bool{}
	for _, x := range a.focus {
		inFocus[x] = true
	}
	var near []string
	for i := len(v.Seps) - 1; i >= 0 && len(near) < 8; i-- {
		s := v.Seps[i]
		if inFocus[s.U] || inFocus[s.V] || len(near) < 3 {
			near = append(near, fmt.Sprintf("c(%d)!=c(%d) for k<=%d", s.U, s.V, s.K))
		}
	}
	fmt.Fprintf(&b, "Verified separations: %d total. Recent/nearby: %s\n", len(v.Seps), strings.Join(near, "; "))
	if len(refs) > 0 {
		b.WriteString("Recent refutations:\n")
		for _, r := range refs {
			fmt.Fprintf(&b, "- %s k=%d by %s: %s\n", r.Kind, r.K, r.Agent, r.Reason)
		}
	}
	if len(leads) > 0 {
		b.WriteString("Leads from other agents:\n")
		for _, l := range leads {
			if l.Kind == proto.KindMerge {
				fmt.Fprintf(&b, "- MERGE (%d,%d) by %s: %s\n", l.U, l.V, l.Agent, l.Note)
			} else {
				fmt.Fprintf(&b, "- %s: %s\n", l.Agent, l.Note)
			}
		}
	}
	if len(outs) > 0 {
		b.WriteString("Your recent moves:\n")
		for _, o := range outs {
			fmt.Fprintf(&b, "- %s -> %s\n", o.Move, o.Result)
		}
	}
	return b.String()
}

func clip(s []int, n int) []int {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Stats for the launcher's summary line.
func (a *Agent) Stats() (published, verified int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.published, a.verified
}
