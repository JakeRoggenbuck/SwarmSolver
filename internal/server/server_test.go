package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/proto"
	"github.com/jakeroggenbuck/swarm/internal/state"
)

// c5 plus a pendant: chi = 3, clique = 2.
func testGraph() *graph.Graph {
	return graph.New("c5", 6, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 0}, {0, 5}})
}

func newTestServer(t *testing.T, logPath string) (*Server, *httptest.Server) {
	g := testGraph()
	s, err := New(g, state.Problem{Name: g.Name, N: g.N, M: g.M}, Config{
		RingSize: 64, Workers: 2, SolverTimeout: 5 * time.Second, LogPath: logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	hs := httptest.NewServer(s.Handler(nil))
	t.Cleanup(hs.Close)
	return s, hs
}

type client struct {
	t *testing.T
	c *websocket.Conn
}

func dial(t *testing.T, hs *httptest.Server, query string) *client {
	u := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws?" + query
	c, _, err := websocket.Dial(context.Background(), u, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(1 << 20)
	t.Cleanup(func() { c.CloseNow() })
	return &client{t, c}
}

func (c *client) send(e proto.Envelope) {
	b, _ := json.Marshal(e)
	if err := c.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// until reads envelopes until pred matches, returning everything read.
func (c *client) until(pred func(*proto.Envelope) bool) []proto.Envelope {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out []proto.Envelope
	for {
		_, data, err := c.c.Read(ctx)
		if err != nil {
			c.t.Fatalf("read: %v (got %d events)", err, len(out))
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var e proto.Envelope
			if err := json.Unmarshal(line, &e); err != nil {
				c.t.Fatal(err)
			}
			out = append(out, e)
			if pred(&e) {
				return out
			}
		}
	}
}

func TestPublishVerifyBounds(t *testing.T) {
	s, hs := newTestServer(t, "")
	watcher := dial(t, hs, "topics=leads,verified,verdicts,state")
	watcher.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindSnapshot })
	agent := dial(t, hs, "topics=verdicts&agent=a1&model=heuristic")

	agent.send(proto.Envelope{Kind: proto.KindColoring, Claim: &proto.Claim{Coloring: []int{0, 1, 0, 1, 2, 1}}})
	evs := watcher.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindBounds })
	var kinds []string
	var last uint64
	for _, e := range evs {
		if e.Seq <= last {
			t.Fatalf("sequence not increasing: %d after %d", e.Seq, last)
		}
		last = e.Seq
		kinds = append(kinds, e.Topic+":"+e.Kind)
	}
	want := "leads:COLORING verdicts:VERDICT verified:COLORING state:BOUNDS"
	if strings.Join(kinds, " ") != want {
		t.Fatalf("events = %v, want %s", kinds, want)
	}
	if b := evs[len(evs)-1].Bounds; b.Upper != 3 || b.UpperAgent != "a1" {
		t.Fatalf("bounds = %+v", b)
	}

	// Same coloring under a relabeling: same content address, dropped as a duplicate.
	agent.send(proto.Envelope{Kind: proto.KindColoring, Claim: &proto.Claim{Coloring: []int{2, 0, 2, 0, 1, 0}}})
	// A bogus coloring is refuted with a reason.
	agent.send(proto.Envelope{Kind: proto.KindColoring, Claim: &proto.Claim{Coloring: []int{0, 0, 1, 0, 1, 1}}})
	v := watcher.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindVerdict })
	if vd := v[len(v)-1].Verdict; vd.Status != proto.StatusRefuted || !strings.Contains(vd.Reason, "monochromatic") {
		t.Fatalf("verdict = %+v", vd)
	}
	if s.M.Dups.Load() != 1 {
		t.Fatalf("dups = %d", s.M.Dups.Load())
	}

	// Lower bound via Z3/native: the 5-cycle is not 2-colorable.
	agent.send(proto.Envelope{Kind: proto.KindSubgraphBound, Claim: &proto.Claim{K: 3, S: []int{0, 1, 2, 3, 4}}})
	evs = watcher.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindBounds })
	if b := evs[len(evs)-1].Bounds; b.Lower != 3 || b.Upper != 3 {
		t.Fatalf("bounds = %+v", b)
	}
	// Stale claims are skipped, not re-verified.
	agent.send(proto.Envelope{Kind: proto.KindClique, Claim: &proto.Claim{S: []int{0, 1}}})
	v = watcher.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindVerdict })
	if vd := v[len(v)-1].Verdict; vd.Status != proto.StatusSkipped {
		t.Fatalf("stale clique verdict = %+v", vd)
	}
	snap := s.Snapshot()
	if snap.Lower != 3 || snap.Upper != 3 || len(snap.Leaderboard) == 0 || snap.Leaderboard[0].Model != "heuristic" {
		t.Fatalf("snapshot = lower %d upper %d lb %+v", snap.Lower, snap.Upper, snap.Leaderboard)
	}
}

func TestSeparationParentsAndResync(t *testing.T) {
	// Path 0-1-2 plus isolated 3: separation c(0)!=c(2) at k=2 makes {0,1,2} a triangle.
	g := graph.New("p", 4, [][2]int{{0, 1}, {1, 2}, {1, 3}, {0, 3}, {2, 3}})
	s, err := New(g, state.Problem{Name: "p", N: 4, M: g.M}, Config{RingSize: 16, Workers: 1, SolverTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	hs := httptest.NewServer(s.Handler(nil))
	defer hs.Close()
	w := dial(t, hs, "topics=verified,verdicts,state")
	w.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindSnapshot })
	a := dial(t, hs, "topics=state&agent=a2")

	// Upper bound first so separations at k=3 are not stale.
	a.send(proto.Envelope{Kind: proto.KindColoring, Claim: &proto.Claim{Coloring: []int{0, 1, 0, 2}}})
	w.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindBounds })
	// G[{0,1,2,3}] with c(0)=c(2): 3 adjacent to 0,1,2 and 1 adjacent to 0,2 -> merged is a triangle + ... not 2-colorable.
	a.send(proto.Envelope{Kind: proto.KindSeparation, Claim: &proto.Claim{K: 2, U: proto.Int(0), V: proto.Int(2), S: []int{0, 1, 2, 3}}})
	evs := w.until(func(e *proto.Envelope) bool { return e.Topic == proto.TopicVerified && e.Kind == proto.KindSeparation })
	sepID := evs[len(evs)-1].ID
	// A bound on {0,1,2} alone is only a path (2-colorable) -> needs the separation edge.
	// k=3 bound needs level-2 separations: the server attaches it as a parent automatically.
	a.send(proto.Envelope{Kind: proto.KindSubgraphBound, Claim: &proto.Claim{K: 3, S: []int{0, 1, 2}}})
	evs = w.until(func(e *proto.Envelope) bool { return e.Topic == proto.TopicVerified && e.Kind == proto.KindSubgraphBound })
	got := evs[len(evs)-1]
	if len(got.Parents) != 1 || got.Parents[0] != sepID {
		t.Fatalf("parents = %v, want [%s]", got.Parents, sepID)
	}
	// Citing an unverified parent is rejected.
	a.send(proto.Envelope{Kind: proto.KindSubgraphBound, Parents: []string{"sha256:nope"}, Claim: &proto.Claim{K: 4, S: []int{0, 1, 2, 3}}})
	evs = w.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindVerdict && e.Verdict.Status == proto.StatusUnknown })

	// Resync: a reader asking for an evicted seq gets a snapshot, not an error.
	for i := 0; i < 40; i++ {
		a.send(proto.Envelope{Kind: proto.KindNote, Note: "filler"})
	}
	time.Sleep(200 * time.Millisecond)
	late := dial(t, hs, "topics=leads&from=1")
	first := late.until(func(e *proto.Envelope) bool { return true })
	if first[0].Kind != proto.KindSnapshot {
		t.Fatalf("evicted replay should start with a snapshot, got %s", first[0].Kind)
	}
}

func TestReplayFromLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	s1, hs1 := newTestServer(t, path)
	w := dial(t, hs1, "topics=state")
	w.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindSnapshot })
	a := dial(t, hs1, "agent=a3")
	a.send(proto.Envelope{Kind: proto.KindColoring, Claim: &proto.Claim{Coloring: []int{0, 1, 0, 1, 2, 1}}})
	w.until(func(e *proto.Envelope) bool { return e.Kind == proto.KindBounds })
	head := s1.Ring.Head()
	hs1.Close()
	time.Sleep(100 * time.Millisecond) // let the sequencer flush

	s2, _ := newTestServer(t, path)
	if s2.Ring.Head() != head {
		t.Fatalf("replayed head %d, want %d", s2.Ring.Head(), head)
	}
	if v := s2.Snapshot(); v.Upper != 3 || v.UpperAgent != "a3" {
		t.Fatalf("replayed state upper=%d agent=%s", v.Upper, v.UpperAgent)
	}
}
