package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/state"
)

// Planner picks the next move from a digest.
type Planner interface {
	Plan(ctx context.Context, digest string) (Plan, error)
}

// Model aliases used by the launcher.
var Models = map[string]string{
	"haiku":  "claude-haiku-4-5",
	"sonnet": "claude-sonnet-5",
	"opus":   "claude-opus-5",
}

// ClaudePlanner asks a Claude model for the next move.
type ClaudePlanner struct {
	Client      *anthropic.Client
	Model       string
	Temperature float64 // only sent to models that accept sampling params
	Role        string
	system      string
}

func NewClaudePlanner(client *anthropic.Client, model string, temperature float64, role string, g *graph.Graph, p state.Problem) *ClaudePlanner {
	return &ClaudePlanner{Client: client, Model: model, Temperature: temperature, Role: role, system: SystemPrompt(g, p, role)}
}

// SystemPrompt is the stable, cacheable part: problem summary, claim
// semantics, and the move menu.
func SystemPrompt(g *graph.Graph, p state.Problem, role string) string {
	maxDeg, minDeg := 0, g.N
	for v := 0; v < g.N; v++ {
		d := g.Degree(v)
		maxDeg = max(maxDeg, d)
		minDeg = min(minDeg, d)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `You are one agent in a swarm of tens of agents attacking the chromatic number chi(G) of the DIMACS graph %s (n=%d vertices, m=%d edges, density %.3f, degree %d..%d).`,
		p.Name, g.N, g.M, g.Density(), minDeg, maxDeg)
	if p.KnownUpper > 0 {
		fmt.Fprintf(&b, ` The best coloring known in the literature uses %d colors.`, p.KnownUpper)
	}
	b.WriteString(`

The swarm shares findings over a realtime log. Every claim carries a small certificate and a Z3 harness checks it before anyone builds on it:
- COLORING (chi <= k): a full coloring. Checked by an edge scan.
- CLIQUE (chi >= k): k pairwise-adjacent vertices.
- SUBGRAPH_BOUND (chi >= k): a vertex set S such that G[S] is not (k-1)-colorable. Z3 proves unsat on |S| vertices only.
- SEPARATION at level k: a pair u,v and set S such that G[S] with c(u)=c(v) is not k-colorable. Then c(u) != c(v) in EVERY k'-coloring for k' <= k, so the pair acts like a new edge. Verified separations are added to everyone's working graph, which makes later bound proofs smaller: a clique of size L plus separation edges can become an (L+1)-clique.
- MERGE: an unverified lead that u,v can share a color.

Refuted claims come back with a counterexample coloring. Duplicate claims are free: identical claims hash to the same id and are checked once.

Each turn you choose ONE move. A fast local Go search (tabu search, DSatur branch-and-bound, greedy growth) executes it within a few seconds and publishes anything it proves:
- "tabucol": look for a coloring with k colors (default: current upper bound - 1). Options: "start" = "best" (drop one color class from the swarm's best coloring and repair; pick it with "drop_class", default the smallest class), "dsatur", or "random".
- "separate": find a separation at level k (default: current lower bound L). Options: "pair" [u,v] of NON-adjacent vertices (good pairs have large N(u) ∪ N(v) containing a big clique), "seed_vertices", "max_size". This is how the lower bound climbs: separations at level L are the stepping stones to proving chi >= L+1.
- "bound": find S proving chi >= k (default: lower bound + 1) using G plus all verified separations at level >= k-1. Options: "seed_vertices" (a dense region or the current witness), "max_size" (20-80).
- "clique": search for a larger clique near "seed_vertices".
- "merge": publish a MERGE lead for "pair" [u,v].

Guidance: the gap closes from both sides. Upper bound work (tabucol) pays when the best coloring has small classes. Lower bound work needs separations first: if few separations exist at the current lower bound level, prefer "separate". If several exist, try "bound". Learn from refutations and your recent move results; do not repeat a move that just failed with identical parameters.
`)
	if role == "strategist" {
		b.WriteString(`
You are a STRATEGIST (a stronger model). Besides your own move, you may set "broadcast": one short sentence of advice to the worker agents (for example which pairs, regions, or color classes look promising). Workers see it in their digest. Only broadcast when you have something specific.
`)
	}
	b.WriteString(`
Reply with ONLY a JSON object, no prose, for example:
{"move":"separate","k":10,"pair":[12,88],"max_size":30,"note":"12 and 88 cover the 10-clique around 40"}
{"move":"tabucol","start":"best","drop_class":3,"note":"class 3 has only 4 vertices"}
{"move":"bound","seed_vertices":[5,17,40],"max_size":50,"note":"dense core near the witness"}
Fields: move (required), k, start, drop_class, pair, seed_vertices, max_size, note (<=200 chars rationale, shown to other agents)` )
	if role == "strategist" {
		b.WriteString(`, broadcast`)
	}
	b.WriteString(".\n")
	return b.String()
}

func (c *ClaudePlanner) Plan(ctx context.Context, digest string) (Plan, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.Model),
		MaxTokens: 2048,
		System: []anthropic.TextBlockParam{{
			Text:         c.system,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(digest + "\nChoose your move. Reply with only the JSON object.")),
		},
	}
	if strings.HasPrefix(c.Model, "claude-haiku") {
		// Haiku accepts sampling params; newer models reject them. Temperature
		// is one of the per-agent diversity knobs.
		params.Temperature = anthropic.Float(c.Temperature)
		params.MaxTokens = 400
	} else {
		// Larger models: keep planning quick and cheap.
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow}
	}
	resp, err := c.Client.Messages.New(ctx, params)
	if err != nil {
		return Plan{}, err
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return Plan{}, fmt.Errorf("model refused")
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return ParsePlan(text.String())
}

// ParsePlan extracts the first JSON object from a model reply.
func ParsePlan(s string) (Plan, error) {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return Plan{}, fmt.Errorf("no JSON object in reply: %.120q", s)
	}
	var p Plan
	if err := json.Unmarshal([]byte(s[i:j+1]), &p); err != nil {
		return Plan{}, fmt.Errorf("bad plan JSON: %v", err)
	}
	switch p.Move {
	case "tabucol", "bound", "separate", "clique", "merge", "dsatur":
	default:
		return Plan{}, fmt.Errorf("unknown move %q", p.Move)
	}
	if len(p.Note) > 200 {
		p.Note = p.Note[:200]
	}
	if len(p.Broadcast) > 200 {
		p.Broadcast = p.Broadcast[:200]
	}
	return p, nil
}
