package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/jakeroggenbuck/swarm/internal/proto"
)

const maxFrame = 512 << 10 // batch envelopes into frames of up to 512 KiB

var anonCounter atomic.Int64

// Handler returns the HTTP mux: /ws, /state, /problem, /metrics, /publish,
// plus the dashboard at / when provided.
func (s *Server) Handler(dashboard http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.serveWS)
	mux.HandleFunc("/state", s.serveState)
	mux.HandleFunc("/problem", s.serveProblem)
	mux.HandleFunc("/metrics", s.serveMetrics)
	mux.HandleFunc("/publish", s.servePublish)
	if dashboard != nil {
		mux.Handle("/", dashboard)
	}
	return mux
}

func (s *Server) serveState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(s.Snapshot().JSON())
}

func (s *Server) serveProblem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.URL.Query().Get("format") == "dimacs" {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, s.G.DIMACS())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Problem any    `json:"problem"`
		DIMACS  string `json:"dimacs"`
	}{s.Problem, s.G.DIMACS()})
}

func (s *Server) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]any{
		"head":          s.Ring.Head(),
		"ring_size":     s.Ring.Size(),
		"clients":       s.M.Clients.Load(),
		"rates":         s.M.rates.Load(),
		"ingest_queue":  len(s.in),
		"ingest_lat_us": s.M.LatencyUs.Load(),
		"resyncs":       s.M.Resyncs.Load(),
		"dups":          s.M.Dups.Load(),
		"rejected":      s.M.Rejected.Load(),
		"goroutines":    runtime.NumGoroutine(),
		"verifier": map[string]any{
			"engine":       s.Checker.Engine(),
			"workers":      s.cfg.Workers,
			"queue":        s.Pool.Depth.Load(),
			"busy":         s.Pool.Busy.Load(),
			"z3_calls":     s.Checker.Z3Calls.Load(),
			"native_calls": s.Checker.NativeCalls.Load(),
			"cache_hits":   s.Checker.CacheHits.Load(),
			"overloaded":   s.M.Overload.Load(),
			"timeout_s":    s.cfg.SolverTimeout.Seconds(),
		},
	})
}

// servePublish lets curl/scripts publish one envelope (or NDJSON batch) over HTTP.
func (s *Server) servePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST an envelope", http.StatusMethodNotAllowed)
		return
	}
	agent := r.URL.Query().Get("agent")
	if agent == "" {
		agent = "http"
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n := s.ingestBatch(agent, body)
	fmt.Fprintf(w, `{"accepted":%d}`+"\n", n)
}

func (s *Server) ingestBatch(agent string, data []byte) int {
	n := 0
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e proto.Envelope
		if err := json.Unmarshal(line, &e); err != nil {
			s.M.Rejected.Add(1)
			continue
		}
		s.Ingest(agent, &e)
		n++
	}
	return n
}

// serveWS: /ws?from=<seq>&topics=leads,verified,verdicts,state&agent=a17&model=haiku
//
// Without from, the client gets a snapshot and then everything after it.
// With from, it replays from that seq (or gets a snapshot if it is evicted).
// Clients publish by sending envelopes (one per line) over the same socket.
func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	c.SetReadLimit(16 << 20)
	q := r.URL.Query()
	mask := proto.ParseTopics(q.Get("topics"))
	agent := q.Get("agent")
	if agent == "" {
		agent = "anon-" + strconv.FormatInt(anonCounter.Add(1), 10)
	}
	if model := q.Get("model"); model != "" {
		s.Hello(agent, model)
	}
	s.M.Clients.Add(1)
	defer s.M.Clients.Add(-1)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Publisher side: read envelopes from this socket into the sequencer.
	go func() {
		defer cancel()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			s.ingestBatch(agent, data)
		}
	}()

	var cursor uint64
	sendSnap := mask&proto.MaskState != 0
	if f := q.Get("from"); f != "" {
		from, _ := strconv.ParseUint(f, 10, 64)
		if from == 0 {
			from = 1
		}
		cursor = from
		if cursor < s.Ring.Oldest() {
			sendSnap = true
			cursor = 0 // resolved to snapshot seq below
		}
	}
	if sendSnap || cursor == 0 {
		snap := s.Snapshot()
		if sendSnap {
			if err := s.writeFrame(ctx, c, snapshotFrame(snap.Seq, snap.JSON()), 1); err != nil {
				return
			}
		}
		if cursor == 0 {
			cursor = snap.Seq + 1
		}
	}
	s.readLoop(ctx, c, cursor, mask)
	c.Close(websocket.StatusNormalClosure, "")
}

func snapshotFrame(seq uint64, state []byte) []byte {
	b := make([]byte, 0, len(state)+96)
	b = fmt.Appendf(b, `{"seq":%d,"topic":"state","kind":"SNAPSHOT","ts":%d,"state":`, seq, time.Now().UnixMilli())
	b = append(b, state...)
	b = append(b, '}', '\n')
	return b
}

// readLoop is the lock-free fan-out path: read head, send ring[cursor..head]
// in one batched write, wait on the generation channel.
func (s *Server) readLoop(ctx context.Context, c *websocket.Conn, cursor uint64, mask uint8) {
	buf := make([]byte, 0, 64<<10)
	for {
		wait := s.Ring.Wait()
		head := s.Ring.Head()
		if cursor > head {
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return
			}
		}
		buf = buf[:0]
		count := 0
		lapped := head-cursor+1 > s.Ring.Size()
		for !lapped && cursor <= head && len(buf) < maxFrame {
			e := s.Ring.Get(cursor)
			if e == nil {
				lapped = true
				break
			}
			if e.Topic&mask != 0 {
				buf = append(buf, e.Data...)
				buf = append(buf, '\n')
				count++
			}
			cursor++
		}
		if lapped {
			// Slow client: skip to a snapshot instead of ever blocking the sequencer.
			s.M.Resyncs.Add(1)
			snap := s.Snapshot()
			if err := s.writeFrame(ctx, c, snapshotFrame(snap.Seq, snap.JSON()), 1); err != nil {
				return
			}
			cursor = snap.Seq + 1
			continue
		}
		if count > 0 {
			if err := s.writeFrame(ctx, c, buf, count); err != nil {
				return
			}
		}
	}
}

func (s *Server) writeFrame(ctx context.Context, c *websocket.Conn, data []byte, count int) error {
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Write(wctx, websocket.MessageText, data); err != nil {
		return err
	}
	s.M.Frames.Add(1)
	s.M.Delivered.Add(int64(count))
	s.M.BytesOut.Add(int64(len(data)))
	return nil
}
