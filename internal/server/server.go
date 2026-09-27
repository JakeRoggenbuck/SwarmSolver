// Package server is the realtime log: agents publish claims over WebSocket,
// a single sequencer orders them, the verifier pool checks them, and every
// client reads the ordered stream from a lock-free ring.
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/jakeroggenbuck/swarm/internal/bus"
	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/proto"
	"github.com/jakeroggenbuck/swarm/internal/state"
	"github.com/jakeroggenbuck/swarm/internal/verify"
)

type Config struct {
	RingSize      int           // power of two
	IngestQueue   int           // capacity of the ingest channel
	Workers       int           // verifier workers
	VerifyQueue   int           // capacity of each verifier lane
	Z3            string        // z3 path, "" = native solver
	SolverTimeout time.Duration // per Z3 call
	MaxS          int           // largest certificate subgraph
	LogPath       string        // optional append-only persistence
	SnapshotEvery time.Duration // max staleness of the published snapshot
}

type msgKind int

const (
	msgPublish msgKind = iota
	msgVerdict
	msgHello
)

type inMsg struct {
	kind    msgKind
	env     *proto.Envelope
	job     verify.Job
	verdict proto.Verdict
	agent   string
	model   string
}

type Server struct {
	cfg     Config
	G       *graph.Graph
	Problem state.Problem
	Ring    *bus.Ring
	Pool    *verify.Pool
	Checker *verify.Checker

	in   chan inMsg
	snap atomic.Pointer[state.View]

	// Sequencer-owned; never touched by other goroutines.
	seq       uint64
	fold      *state.Fold
	seen      map[string]string // claim id -> "" (pending) or verdict status
	pending   map[string]*proto.Envelope
	seps      []state.Sep
	sepByID   map[string]state.Sep
	logw      *bufio.Writer
	logf      *os.File
	lastSnap  time.Time
	forceSnap bool

	M Metrics
}

// Metrics are plain atomics; a sampler turns them into rates.
type Metrics struct {
	Ingested  atomic.Int64 // messages received from clients
	Appended  atomic.Int64 // envelopes written to the log
	Delivered atomic.Int64 // envelopes written to sockets (sum over clients)
	Frames    atomic.Int64 // websocket frames written
	BytesOut  atomic.Int64
	Clients   atomic.Int64
	Resyncs   atomic.Int64 // slow clients that were jumped to a snapshot
	Dups      atomic.Int64
	Overload  atomic.Int64
	Rejected  atomic.Int64
	LatencyUs atomic.Int64 // EWMA of ingest -> sequenced latency, microseconds
	rates     atomic.Pointer[Rates]
}

type Rates struct {
	IngestPerSec    float64 `json:"ingest_per_sec"`
	AppendPerSec    float64 `json:"append_per_sec"`
	DeliveredPerSec float64 `json:"delivered_per_sec"`
	FramesPerSec    float64 `json:"frames_per_sec"`
	BytesPerSec     float64 `json:"bytes_out_per_sec"`
}

func New(g *graph.Graph, p state.Problem, cfg Config) (*Server, error) {
	if cfg.RingSize == 0 {
		cfg.RingSize = 1 << 16
	}
	if cfg.IngestQueue == 0 {
		cfg.IngestQueue = 1 << 16
	}
	if cfg.VerifyQueue == 0 {
		cfg.VerifyQueue = 4096
	}
	if cfg.SnapshotEvery == 0 {
		cfg.SnapshotEvery = 50 * time.Millisecond
	}
	if cfg.MaxS == 0 {
		cfg.MaxS = 160
	}
	now := time.Now().UnixMilli()
	s := &Server{
		cfg: cfg, G: g, Problem: p,
		Ring:    bus.NewRing(cfg.RingSize),
		in:      make(chan inMsg, cfg.IngestQueue),
		fold:    state.NewFold(p, now),
		seen:    map[string]string{},
		pending: map[string]*proto.Envelope{},
		sepByID: map[string]state.Sep{},
	}
	s.Checker = &verify.Checker{G: g, Z3: cfg.Z3, Timeout: cfg.SolverTimeout, MaxS: cfg.MaxS}
	s.Pool = verify.NewPool(s.Checker, cfg.Workers, cfg.VerifyQueue, func(j verify.Job, v proto.Verdict) {
		s.in <- inMsg{kind: msgVerdict, job: j, verdict: v}
	})
	s.Pool.Skip = s.stale
	s.M.rates.Store(&Rates{})

	if cfg.LogPath != "" {
		if err := s.replay(cfg.LogPath); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		s.logf = f
		s.logw = bufio.NewWriterSize(f, 1<<20)
	}
	s.snap.Store(s.fold.Snapshot(s.seq))
	s.Ring.Publish(s.seq)
	return s, nil
}

// Start launches the sequencer and the metrics sampler.
func (s *Server) Start() {
	go s.sequencer()
	go s.sampler()
}

// Snapshot returns the latest immutable state view.
func (s *Server) Snapshot() *state.View { return s.snap.Load() }

// Ingest hands a client message to the sequencer. It blocks when the ingest
// queue is full, which back-pressures only the publishing client.
func (s *Server) Ingest(agent string, e *proto.Envelope) {
	e.Agent = agent
	s.M.Ingested.Add(1)
	s.in <- inMsg{kind: msgPublish, env: e}
}

func (s *Server) Hello(agent, model string) {
	s.in <- inMsg{kind: msgHello, agent: agent, model: model}
}

// sequencer is the only writer to the ring and the only owner of the fold.
func (s *Server) sequencer() {
	tick := time.NewTicker(s.cfg.SnapshotEvery)
	defer tick.Stop()
	published := s.seq
	for {
		select {
		case m := <-s.in:
			s.handle(m)
			// Drain whatever else is queued so one wake-up covers a whole batch.
		drain:
			for i := 0; i < 4096; i++ {
				select {
				case m = <-s.in:
					s.handle(m)
				default:
					break drain
				}
			}
		case <-tick.C:
		}
		if s.fold.Dirty() && (s.forceSnap || time.Since(s.lastSnap) >= s.cfg.SnapshotEvery) {
			s.snap.Store(s.fold.Snapshot(s.seq))
			s.lastSnap = time.Now()
			s.forceSnap = false
		}
		if s.seq != published {
			if s.logw != nil {
				s.logw.Flush()
			}
			s.Ring.Publish(s.seq)
			published = s.seq
		}
	}
}

func (s *Server) handle(m inMsg) {
	switch m.kind {
	case msgPublish:
		s.handlePublish(m.env)
	case msgVerdict:
		s.handleVerdict(m.job, m.verdict)
	case msgHello:
		s.fold.SetModel(m.agent, m.model)
	}
}

// append stamps seq, serializes once, and stores into the ring.
func (s *Server) append(e *proto.Envelope) {
	s.seq++
	e.Seq = s.seq
	if e.TS == 0 {
		e.TS = time.Now().UnixMilli()
	}
	data, err := json.Marshal(e)
	if err != nil {
		log.Printf("encode seq %d: %v", e.Seq, err)
		data = []byte(fmt.Sprintf(`{"seq":%d,"topic":"leads","kind":"NOTE","note":"encode error"}`, e.Seq))
	}
	s.Ring.Put(&bus.Entry{Seq: e.Seq, Topic: proto.TopicMask(e.Topic), Data: data})
	if s.logw != nil {
		s.logw.Write(data)
		s.logw.WriteByte('\n')
	}
	s.M.Appended.Add(1)
}

func (s *Server) handlePublish(e *proto.Envelope) {
	now := time.Now()
	if e.SentTS > 0 {
		lat := now.UnixMicro() - e.SentTS*1000
		if lat >= 0 {
			old := s.M.LatencyUs.Load()
			s.M.LatencyUs.Store((old*7 + lat) / 8)
		}
	}
	e.TS = now.UnixMilli()
	e.Topic = proto.TopicLeads
	e.Seq = 0
	e.Verdict, e.Bounds, e.State = nil, nil, nil
	if len(e.Note) > 200 {
		e.Note = e.Note[:200]
	}
	if e.Kind == proto.KindNote {
		e.ID, e.Claim = "", nil
		s.append(e)
		s.fold.Lead(e.Agent)
		return
	}
	if err := proto.Normalize(e.Kind, e.Claim); err != nil {
		s.M.Rejected.Add(1)
		v := proto.Verdict{Kind: e.Kind, Agent: e.Agent, Status: proto.StatusRefuted, Reason: "malformed claim: " + err.Error()}
		s.append(&proto.Envelope{Topic: proto.TopicVerdicts, Kind: proto.KindVerdict, Agent: e.Agent, Verdict: &v})
		s.fold.Verdict(&v)
		return
	}
	id := proto.ClaimID(e.Kind, e.Claim)
	if _, dup := s.seen[id]; dup {
		// Another agent already found this exact claim; it is verified once.
		s.M.Dups.Add(1)
		s.fold.Dup()
		return
	}
	s.seen[id] = ""
	e.ID = id
	s.append(e)
	s.fold.Lead(e.Agent)
	if e.Kind == proto.KindMerge {
		return // heuristic lead only, never verified on its own
	}

	job := verify.Job{ID: id, Kind: e.Kind, Agent: e.Agent, Claim: *e.Claim, Enqueued: now}
	if skip, why := s.staleAgainst(job, s.fold.Upper(), s.fold.Lower()); skip {
		s.handleVerdict(job, proto.Verdict{Of: id, Kind: e.Kind, K: e.Claim.K, Agent: e.Agent, Status: proto.StatusSkipped, Reason: why})
		return
	}
	extra, parents, err := s.resolveParents(e)
	if err != nil {
		s.handleVerdict(job, proto.Verdict{Of: id, Kind: e.Kind, K: e.Claim.K, Agent: e.Agent, Status: proto.StatusUnknown, Reason: err.Error()})
		return
	}
	e.Parents = parents
	job.Extra = extra
	s.pending[id] = e
	if !s.Pool.Submit(job) {
		s.M.Overload.Add(1)
		delete(s.pending, id)
		delete(s.seen, id) // allow a retry later
		v := proto.Verdict{Of: id, Kind: e.Kind, K: e.Claim.K, Agent: e.Agent, Status: proto.StatusUnknown, Reason: "verifier queue full"}
		s.append(&proto.Envelope{Topic: proto.TopicVerdicts, Kind: proto.KindVerdict, Agent: e.Agent, ID: id, Verdict: &v})
		s.fold.Verdict(&v)
	}
}

// resolveParents checks that cited parents are verified, and collects the
// edges implied by verified separations inside S. Separations the claim did
// not cite but that apply are added automatically (sound either way), so the
// provenance DAG records every insight the proof actually used.
func (s *Server) resolveParents(e *proto.Envelope) ([][2]int, []string, error) {
	var need int
	switch e.Kind {
	case proto.KindSubgraphBound:
		need = e.Claim.K - 1 // a (k-1)-coloring respects separations proven at level >= k-1
	case proto.KindSeparation:
		need = e.Claim.K
	default:
		for _, p := range e.Parents {
			if s.fold.Finding(p) == nil {
				return nil, nil, fmt.Errorf("parent %s is not a verified finding", p)
			}
		}
		return nil, e.Parents, nil
	}
	parents := map[string]bool{}
	var order []string
	for _, p := range e.Parents {
		if s.fold.Finding(p) == nil {
			return nil, nil, fmt.Errorf("parent %s is not a verified finding", p)
		}
		if sep, ok := s.sepByID[p]; ok && sep.K < need {
			return nil, nil, fmt.Errorf("parent separation %s holds for k<=%d, claim needs k=%d", p, sep.K, need)
		}
		if !parents[p] {
			parents[p] = true
			order = append(order, p)
		}
	}
	inS := make(map[int]bool, len(e.Claim.S))
	for _, v := range e.Claim.S {
		inS[v] = true
	}
	var extra [][2]int
	for _, sep := range s.seps {
		if sep.K < need || !inS[sep.U] || !inS[sep.V] {
			continue
		}
		extra = append(extra, [2]int{sep.U, sep.V})
		if !parents[sep.ID] {
			parents[sep.ID] = true
			order = append(order, sep.ID)
		}
	}
	return extra, order, nil
}

func (s *Server) handleVerdict(j verify.Job, v proto.Verdict) {
	env := s.pending[j.ID]
	delete(s.pending, j.ID)
	s.seen[j.ID] = v.Status
	s.append(&proto.Envelope{Topic: proto.TopicVerdicts, Kind: proto.KindVerdict, Agent: v.Agent, ID: j.ID, Verdict: &v})
	s.fold.Verdict(&v)
	if v.Status != proto.StatusVerified || env == nil {
		return
	}
	ve := &proto.Envelope{Topic: proto.TopicVerified, Agent: env.Agent, Kind: env.Kind, ID: env.ID,
		Parents: env.Parents, Claim: env.Claim, Note: env.Note}
	s.append(ve)
	if ve.Kind == proto.KindSeparation {
		sep := state.Sep{ID: ve.ID, U: *ve.Claim.U, V: *ve.Claim.V, K: ve.Claim.K, Size: len(ve.Claim.S), Agent: ve.Agent}
		s.seps = append(s.seps, sep)
		s.sepByID[ve.ID] = sep
	}
	if b := s.fold.Apply(ve); b != nil {
		s.append(&proto.Envelope{Topic: proto.TopicState, Kind: proto.KindBounds, Agent: ve.Agent, ID: ve.ID, Bounds: b})
		s.forceSnap = true
		log.Printf("bounds %d <= chi <= %d  (%s %s by %s)", b.Lower, b.Upper, ve.Kind, ve.ID, ve.Agent)
	}
}

// stale reports claims that can no longer improve a bound. Called by
// verifier workers against the published snapshot.
func (s *Server) stale(j verify.Job) (bool, string) {
	v := s.snap.Load()
	return s.staleAgainst(j, v.Upper, v.Lower)
}

func (s *Server) staleAgainst(j verify.Job, upper, lower int) (bool, string) {
	switch j.Kind {
	case proto.KindColoring:
		if j.Claim.K >= upper {
			return true, fmt.Sprintf("a %d-coloring is already known", upper)
		}
	case proto.KindClique, proto.KindSubgraphBound:
		if j.Claim.K <= lower {
			return true, fmt.Sprintf("chi >= %d is already proven", lower)
		}
	case proto.KindSeparation:
		if j.Claim.K >= upper {
			return true, fmt.Sprintf("separations at k=%d cannot help: a %d-coloring exists", j.Claim.K, upper)
		}
	}
	return false, ""
}

// replay restores the log after a restart.
func (s *Server) replay(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	n := 0
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		var e proto.Envelope
		if json.Unmarshal(line, &e) != nil || e.Seq != s.seq+1 {
			continue
		}
		s.seq = e.Seq
		s.Ring.Put(&bus.Entry{Seq: e.Seq, Topic: proto.TopicMask(e.Topic), Data: line})
		switch e.Topic {
		case proto.TopicLeads:
			s.fold.Lead(e.Agent)
			if e.ID != "" {
				s.seen[e.ID] = ""
			}
		case proto.TopicVerdicts:
			if e.Verdict != nil {
				s.fold.Verdict(e.Verdict)
				if e.ID != "" {
					s.seen[e.ID] = e.Verdict.Status
				}
			}
		case proto.TopicVerified:
			if e.Kind == proto.KindSeparation && e.Claim != nil && e.Claim.U != nil {
				sep := state.Sep{ID: e.ID, U: *e.Claim.U, V: *e.Claim.V, K: e.Claim.K, Size: len(e.Claim.S), Agent: e.Agent}
				s.seps = append(s.seps, sep)
				s.sepByID[e.ID] = sep
			}
			s.fold.Apply(&e)
		}
		n++
	}
	// Claims that were in flight when we stopped: let agents resubmit them.
	for id, st := range s.seen {
		if st == "" {
			delete(s.seen, id)
		}
	}
	if n > 0 {
		log.Printf("replayed %d events from %s (seq %d, bounds %d..%d)", n, path, s.seq, s.fold.Lower(), s.fold.Upper())
	}
	return sc.Err()
}

func (s *Server) sampler() {
	type snap struct{ in, app, del, fr, by int64 }
	read := func() snap {
		return snap{s.M.Ingested.Load(), s.M.Appended.Load(), s.M.Delivered.Load(), s.M.Frames.Load(), s.M.BytesOut.Load()}
	}
	prev, t0 := read(), time.Now()
	for range time.Tick(time.Second) {
		cur, t1 := read(), time.Now()
		dt := t1.Sub(t0).Seconds()
		s.M.rates.Store(&Rates{
			IngestPerSec:    float64(cur.in-prev.in) / dt,
			AppendPerSec:    float64(cur.app-prev.app) / dt,
			DeliveredPerSec: float64(cur.del-prev.del) / dt,
			FramesPerSec:    float64(cur.fr-prev.fr) / dt,
			BytesPerSec:     float64(cur.by-prev.by) / dt,
		})
		prev, t0 = cur, t1
	}
}
