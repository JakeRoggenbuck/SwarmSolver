// Package state folds verified events into the current best view of the
// problem. The fold runs on the sequencer goroutine; readers only ever see
// immutable View snapshots published through an atomic pointer.
package state

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/jakeroggenbuck/swarm/internal/proto"
)

// Problem describes the instance.
type Problem struct {
	Name       string  `json:"name"`
	N          int     `json:"n"`
	M          int     `json:"m"`
	Density    float64 `json:"density"`
	KnownUpper int     `json:"known_upper,omitempty"` // best coloring in the literature
	KnownLower int     `json:"known_lower,omitempty"` // best proven lower bound in the literature
	Note       string  `json:"note,omitempty"`
}

// Sep is a verified separation: an edge every k'-coloring (k' <= K) must respect.
type Sep struct {
	ID    string `json:"id"`
	U     int    `json:"u"`
	V     int    `json:"v"`
	K     int    `json:"k"`
	Size  int    `json:"size"`
	Agent string `json:"agent"`
}

// Finding is a node of the provenance DAG.
type Finding struct {
	ID      string   `json:"id"`
	Seq     uint64   `json:"seq"`
	Kind    string   `json:"kind"`
	K       int      `json:"k"`
	Agent   string   `json:"agent"`
	TS      int64    `json:"ts"`
	Parents []string `json:"parents,omitempty"`
	Improve bool     `json:"improve,omitempty"` // moved a bound
	Size    int      `json:"size,omitempty"`
}

type AgentStat struct {
	Agent     string `json:"agent"`
	Model     string `json:"model,omitempty"`
	Published int    `json:"published"`
	Verified  int    `json:"verified"`
	Refuted   int    `json:"refuted"`
	Unknown   int    `json:"unknown"`
	Improved  int    `json:"improved"` // bound moves
	Cited     int    `json:"cited"`    // times other verified findings built on this agent's work
	Score     int    `json:"score"`
}

type Counts struct {
	Leads    int `json:"leads"`
	Verified int `json:"verified"`
	Refuted  int `json:"refuted"`
	Unknown  int `json:"unknown"`
	Skipped  int `json:"skipped"`
	Dups     int `json:"dups"`
}

type Point struct {
	TS    int64  `json:"ts"`
	Seq   uint64 `json:"seq"`
	Upper int    `json:"upper"`
	Lower int    `json:"lower"`
}

// View is an immutable snapshot.
type View struct {
	Seq          uint64      `json:"seq"`
	StartedAt    int64       `json:"started_at"`
	Problem      Problem     `json:"problem"`
	Upper        int         `json:"upper"`
	UpperID      string      `json:"upper_id,omitempty"`
	UpperAgent   string      `json:"upper_agent,omitempty"`
	BestColoring []int       `json:"best_coloring,omitempty"`
	Lower        int         `json:"lower"`
	LowerID      string      `json:"lower_id,omitempty"`
	LowerAgent   string      `json:"lower_agent,omitempty"`
	LowerKind    string      `json:"lower_kind,omitempty"`
	LowerWitness []int       `json:"lower_witness,omitempty"`
	Separations  []Sep       `json:"separations"`
	Counts       Counts      `json:"counts"`
	Leaderboard  []AgentStat `json:"leaderboard"`
	History      []Point     `json:"history"`
	Findings     []Finding   `json:"findings"`

	once sync.Once
	enc  []byte
}

// JSON encodes the view once, lazily, and caches the bytes.
func (v *View) JSON() []byte {
	v.once.Do(func() { v.enc, _ = json.Marshal(v) })
	return v.enc
}

// Fold is the mutable state owned by the sequencer goroutine.
type Fold struct {
	v        View
	agents   map[string]*AgentStat
	findings map[string]*Finding
	sepSeen  map[[3]int]bool
	dirty    bool
}

const maxFindings = 400

func NewFold(p Problem, startedAt int64) *Fold {
	f := &Fold{agents: map[string]*AgentStat{}, findings: map[string]*Finding{}, sepSeen: map[[3]int]bool{}}
	f.v.Problem = p
	f.v.StartedAt = startedAt
	f.v.Upper = p.N // trivially chi <= N
	f.v.Lower = 0
	if p.N > 0 {
		f.v.Lower = 1
	}
	if p.M > 0 {
		f.v.Lower = 2
	}
	f.v.History = []Point{{TS: startedAt, Upper: f.v.Upper, Lower: f.v.Lower}}
	f.dirty = true
	return f
}

func (f *Fold) agent(name string) *AgentStat {
	a := f.agents[name]
	if a == nil {
		a = &AgentStat{Agent: name}
		f.agents[name] = a
	}
	return a
}

func (f *Fold) Upper() int { return f.v.Upper }
func (f *Fold) Lower() int { return f.v.Lower }

// SetModel records which LLM an agent runs (from its hello).
func (f *Fold) SetModel(agent, model string) {
	f.agent(agent).Model = model
	f.dirty = true
}

func (f *Fold) Lead(agent string) {
	f.v.Counts.Leads++
	f.agent(agent).Published++
	f.dirty = true
}

func (f *Fold) Dup() { f.v.Counts.Dups++; f.dirty = true }

// Verdict tallies an outcome.
func (f *Fold) Verdict(v *proto.Verdict) {
	a := f.agent(v.Agent)
	switch v.Status {
	case proto.StatusVerified:
		f.v.Counts.Verified++
		a.Verified++
	case proto.StatusRefuted:
		f.v.Counts.Refuted++
		a.Refuted++
	case proto.StatusUnknown:
		f.v.Counts.Unknown++
		a.Unknown++
	case proto.StatusSkipped:
		f.v.Counts.Skipped++
	}
	f.dirty = true
}

// Apply folds a verified claim. Returns bounds if a bound moved.
func (f *Fold) Apply(e *proto.Envelope) *proto.Bounds {
	c := e.Claim
	fd := &Finding{ID: e.ID, Seq: e.Seq, Kind: e.Kind, K: c.K, Agent: e.Agent, TS: e.TS, Parents: e.Parents, Size: len(c.S)}
	var moved bool
	switch e.Kind {
	case proto.KindColoring:
		if c.K < f.v.Upper {
			f.v.Upper, f.v.UpperID, f.v.UpperAgent = c.K, e.ID, e.Agent
			f.v.BestColoring = c.Coloring
			moved = true
		}
		fd.Size = len(c.Coloring)
	case proto.KindClique, proto.KindSubgraphBound:
		if c.K > f.v.Lower {
			f.v.Lower, f.v.LowerID, f.v.LowerAgent, f.v.LowerKind = c.K, e.ID, e.Agent, e.Kind
			f.v.LowerWitness = c.S
			moved = true
		}
	case proto.KindSeparation:
		key := [3]int{*c.U, *c.V, c.K}
		if !f.sepSeen[key] {
			f.sepSeen[key] = true
			f.v.Separations = append(f.v.Separations, Sep{ID: e.ID, U: *c.U, V: *c.V, K: c.K, Size: len(c.S), Agent: e.Agent})
		}
	}
	fd.Improve = moved
	f.findings[e.ID] = fd
	f.v.Findings = append(f.v.Findings, *fd)
	if len(f.v.Findings) > maxFindings {
		f.v.Findings = f.v.Findings[len(f.v.Findings)-maxFindings:]
	}
	a := f.agent(e.Agent)
	// Credit parents' authors when someone else builds on their work.
	for _, p := range e.Parents {
		if pf := f.findings[p]; pf != nil && pf.Agent != e.Agent {
			f.agent(pf.Agent).Cited++
		}
	}
	f.dirty = true
	if !moved {
		return nil
	}
	a.Improved++
	f.v.History = append(f.v.History, Point{TS: e.TS, Seq: e.Seq, Upper: f.v.Upper, Lower: f.v.Lower})
	return &proto.Bounds{Upper: f.v.Upper, Lower: f.v.Lower, UpperAgent: f.v.UpperAgent,
		LowerAgent: f.v.LowerAgent, LowerKind: f.v.LowerKind, Cause: e.ID}
}

// IsVerified reports whether id is a verified finding, and returns it.
func (f *Fold) Finding(id string) *Finding { return f.findings[id] }

// Dirty reports whether Snapshot would differ from the last one.
func (f *Fold) Dirty() bool { return f.dirty }

// Snapshot copies the fold into a fresh immutable View.
func (f *Fold) Snapshot(seq uint64) *View {
	f.dirty = false
	v := &View{
		Seq: seq, StartedAt: f.v.StartedAt, Problem: f.v.Problem,
		Upper: f.v.Upper, UpperID: f.v.UpperID, UpperAgent: f.v.UpperAgent, BestColoring: f.v.BestColoring,
		Lower: f.v.Lower, LowerID: f.v.LowerID, LowerAgent: f.v.LowerAgent, LowerKind: f.v.LowerKind, LowerWitness: f.v.LowerWitness,
		Separations: append([]Sep{}, f.v.Separations...),
		Counts:      f.v.Counts,
		History:     append([]Point{}, f.v.History...),
		Findings:    append([]Finding{}, f.v.Findings...),
	}
	lb := make([]AgentStat, 0, len(f.agents))
	for _, a := range f.agents {
		s := *a
		s.Score = s.Verified + 10*s.Improved + 3*s.Cited
		lb = append(lb, s)
	}
	sort.Slice(lb, func(i, j int) bool {
		if lb[i].Score != lb[j].Score {
			return lb[i].Score > lb[j].Score
		}
		return lb[i].Agent < lb[j].Agent
	})
	v.Leaderboard = lb
	return v
}
