// swarm-tail prints the event log in human-readable form.
//
//	swarm-tail                         # live, all topics
//	swarm-tail -from 1 -topics verdicts # replay every verdict from the start
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/jakeroggenbuck/swarm/internal/proto"
)

func main() {
	server := flag.String("server", "http://localhost:8080", "swarm server URL")
	topics := flag.String("topics", "leads,verified,verdicts,state", "topics")
	from := flag.Int64("from", 0, "replay from this seq (0 = live)")
	raw := flag.Bool("json", false, "print raw envelopes")
	notes := flag.Bool("notes", false, "include NOTE leads")
	exit := flag.Bool("exit", false, "exit after the replay catches up (1s idle)")
	flag.Parse()

	u, _ := url.Parse(strings.TrimRight(*server, "/") + "/ws")
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	q := url.Values{"topics": {*topics}, "agent": {"tail"}}
	if *from > 0 {
		q.Set("from", fmt.Sprint(*from))
	}
	u.RawQuery = q.Encode()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c, _, err := websocket.Dial(ctx, u.String(), nil)
	if err != nil {
		log.Fatal(err)
	}
	c.SetReadLimit(64 << 20)
	for {
		rctx := ctx
		var cancel context.CancelFunc = func() {}
		if *exit {
			rctx, cancel = context.WithTimeout(ctx, time.Second)
		}
		_, data, err := c.Read(rctx)
		cancel()
		if err != nil {
			return
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			if *raw {
				fmt.Println(string(line))
				continue
			}
			var e proto.Envelope
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			if e.Kind == proto.KindNote && !*notes {
				continue
			}
			fmt.Println(format(&e))
		}
	}
}

func format(e *proto.Envelope) string {
	t := time.UnixMilli(e.TS).Format("15:04:05.000")
	head := fmt.Sprintf("%s %6d %-9s %-10s", t, e.Seq, e.Topic, e.Agent)
	c := e.Claim
	switch e.Kind {
	case proto.KindSnapshot:
		var v struct{ Lower, Upper int }
		json.Unmarshal(e.State, &v)
		return fmt.Sprintf("%s SNAPSHOT %d <= chi <= %d", head, v.Lower, v.Upper)
	case proto.KindBounds:
		return fmt.Sprintf("%s *** BOUNDS %d <= chi <= %d", head, e.Bounds.Lower, e.Bounds.Upper)
	case proto.KindVerdict:
		v := e.Verdict
		return fmt.Sprintf("%s %-8s %s k=%d  %s %.0fms core=%d  %s", head, v.Status, v.Kind, v.K, v.Engine, v.Ms, v.Core, v.Reason)
	case proto.KindColoring:
		return fmt.Sprintf("%s COLORING chi <= %d  %s", head, c.K, e.Note)
	case proto.KindClique:
		return fmt.Sprintf("%s CLIQUE chi >= %d", head, c.K)
	case proto.KindSubgraphBound:
		return fmt.Sprintf("%s SUBGRAPH_BOUND chi >= %d |S|=%d parents=%d  %s", head, c.K, len(c.S), len(e.Parents), e.Note)
	case proto.KindSeparation:
		return fmt.Sprintf("%s SEPARATION c(%d)!=c(%d) k<=%d |S|=%d  %s", head, *c.U, *c.V, c.K, len(c.S), e.Note)
	case proto.KindMerge:
		return fmt.Sprintf("%s MERGE (%d,%d)  %s", head, *c.U, *c.V, e.Note)
	case proto.KindNote:
		return fmt.Sprintf("%s NOTE %s", head, e.Note)
	}
	return head + " " + e.Kind
}
