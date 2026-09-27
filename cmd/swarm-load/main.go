// swarm-load is a synthetic load client: N subscriber sockets plus P
// publishers sending NOTE leads at a fixed rate. It reports fan-out
// throughput and end-to-end latency (publish -> delivered to a subscriber).
//
// Run it against a scratch server; the NOTE traffic lands in the log.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/jakeroggenbuck/swarm/internal/proto"
)

func main() {
	var (
		server     = flag.String("server", "http://localhost:8080", "swarm server URL")
		clients    = flag.Int("clients", 500, "subscriber connections")
		publishers = flag.Int("publishers", 20, "publisher connections")
		rate       = flag.Float64("rate", 50, "messages/sec per publisher")
		batch      = flag.Int("batch", 1, "envelopes per publish frame")
		duration   = flag.Duration("duration", 20*time.Second, "measurement duration")
		sample     = flag.Int("sample", 8, "record latency for 1 in N deliveries")
	)
	flag.Parse()

	u, _ := url.Parse(strings.TrimRight(*server, "/") + "/ws")
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		delivered atomic.Int64
		frames    atomic.Int64
		sent      atomic.Int64
		errs      atomic.Int64
		connected atomic.Int64
		mu        sync.Mutex
		lats      []float64 // milliseconds
	)

	// Subscribers.
	var wg sync.WaitGroup
	for i := 0; i < *clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, _, err := websocket.Dial(ctx, u.String()+"?topics=leads&agent=load-sub-"+strconv.Itoa(i), &websocket.DialOptions{HTTPClient: &http.Client{}})
			if err != nil {
				errs.Add(1)
				return
			}
			c.SetReadLimit(64 << 20)
			defer c.CloseNow()
			connected.Add(1)
			var local []float64
			n := 0
			for {
				_, data, err := c.Read(ctx)
				if err != nil {
					break
				}
				frames.Add(1)
				now := time.Now().UnixMicro()
				for _, line := range bytes.Split(data, []byte{'\n'}) {
					if len(line) == 0 {
						continue
					}
					delivered.Add(1)
					n++
					if n%*sample != 0 {
						continue
					}
					// Note carries the publish time in microseconds.
					if j := bytes.Index(line, []byte(`"note":"t=`)); j >= 0 {
						rest := line[j+10:]
						if k := bytes.IndexByte(rest, '"'); k > 0 {
							if t, err := strconv.ParseInt(string(rest[:k]), 10, 64); err == nil {
								local = append(local, float64(now-t)/1000)
							}
						}
					}
				}
			}
			mu.Lock()
			lats = append(lats, local...)
			mu.Unlock()
		}(i)
		if i%50 == 49 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	// Wait for subscribers to connect.
	deadline := time.Now().Add(15 * time.Second)
	for connected.Load()+errs.Load() < int64(*clients) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	log.Printf("%d subscribers connected (%d failed)", connected.Load(), errs.Load())

	// Publishers.
	pctx, pcancel := context.WithTimeout(ctx, *duration)
	defer pcancel()
	var pwg sync.WaitGroup
	for i := 0; i < *publishers; i++ {
		pwg.Add(1)
		go func(i int) {
			defer pwg.Done()
			c, _, err := websocket.Dial(ctx, u.String()+"?topics=state&agent=load-pub-"+strconv.Itoa(i), nil)
			if err != nil {
				errs.Add(1)
				return
			}
			defer c.CloseNow()
			go func() { // drain the snapshot so the socket stays healthy
				for {
					if _, _, err := c.Read(ctx); err != nil {
						return
					}
				}
			}()
			interval := time.Duration(float64(time.Second) * float64(*batch) / *rate)
			t := time.NewTicker(interval)
			defer t.Stop()
			var buf bytes.Buffer
			for {
				select {
				case <-pctx.Done():
					return
				case <-t.C:
				}
				buf.Reset()
				for b := 0; b < *batch; b++ {
					e := proto.Envelope{Kind: proto.KindNote, Note: "t=" + strconv.FormatInt(time.Now().UnixMicro(), 10)}
					d, _ := json.Marshal(e)
					buf.Write(d)
					buf.WriteByte('\n')
				}
				if err := c.Write(ctx, websocket.MessageText, buf.Bytes()); err != nil {
					errs.Add(1)
					return
				}
				sent.Add(int64(*batch))
			}
		}(i)
	}

	start := time.Now()
	t := time.NewTicker(2 * time.Second)
	prevD, prevS := int64(0), int64(0)
	for {
		select {
		case <-t.C:
			d, s := delivered.Load(), sent.Load()
			log.Printf("published %6.0f/s   delivered %9.0f/s   frames %d", float64(s-prevS)/2, float64(d-prevD)/2, frames.Load())
			prevD, prevS = d, s
			continue
		case <-pctx.Done():
		}
		break
	}
	pwg.Wait()
	elapsed := time.Since(start)
	time.Sleep(500 * time.Millisecond) // let the tail drain
	cancel()
	wg.Wait()

	sort.Float64s(lats)
	pct := func(p float64) float64 {
		if len(lats) == 0 {
			return 0
		}
		return lats[min(len(lats)-1, int(p*float64(len(lats))))]
	}
	fmt.Println()
	fmt.Printf("subscribers        %d\n", connected.Load())
	fmt.Printf("publishers         %d x %.0f msg/s\n", *publishers, *rate)
	fmt.Printf("published          %d  (%.0f/s)\n", sent.Load(), float64(sent.Load())/elapsed.Seconds())
	fmt.Printf("delivered          %d  (%.0f/s fan-out)\n", delivered.Load(), float64(delivered.Load())/elapsed.Seconds())
	fmt.Printf("frames             %d  (%.1f envelopes/frame)\n", frames.Load(), float64(delivered.Load())/float64(max(1, frames.Load())))
	fmt.Printf("latency p50/p90/p99/max  %.2f / %.2f / %.2f / %.2f ms  (%d samples)\n", pct(0.5), pct(0.9), pct(0.99), pct(1), len(lats))
	fmt.Printf("errors             %d\n", errs.Load())
	if resp, err := http.Get(strings.TrimRight(*server, "/") + "/metrics"); err == nil {
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		resp.Body.Close()
		fmt.Printf("server resyncs     %v (slow clients jumped to a snapshot)\n", m["resyncs"])
	}
}
