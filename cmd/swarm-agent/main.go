// swarm-agent launches a swarm of agents in one process, each with its own
// WebSocket connection, random seed, strategy, and (optionally) Claude model.
//
//	swarm-agent -heuristic 12                 # no LLM, pure local search
//	swarm-agent -haiku 16 -sonnet 3 -opus 1   # Claude-planned swarm
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/jakeroggenbuck/swarm/internal/agent"
	"github.com/jakeroggenbuck/swarm/internal/state"
)

func main() {
	var (
		server    = flag.String("server", "http://localhost:8080", "swarm server URL")
		heuristic = flag.Int("heuristic", 0, "heuristic-only agents (no LLM)")
		haiku     = flag.Int("haiku", 0, "Claude Haiku worker agents")
		sonnet    = flag.Int("sonnet", 0, "Claude Sonnet strategist agents")
		opus      = flag.Int("opus", 0, "Claude Opus strategist agents")
		opusModel = flag.String("opus-model", agent.Models["opus"], "model id for opus strategists")
		budget    = flag.Duration("budget", 4*time.Second, "local compute budget per move")
		seed      = flag.Int64("seed", time.Now().UnixNano(), "base random seed")
		duration  = flag.Duration("duration", 0, "stop after this long (0 = run until Ctrl-C)")
		verbose   = flag.Bool("v", false, "log every agent turn")
		prefix    = flag.String("prefix", "", "agent name prefix (to run several launchers)")
	)
	flag.Parse()
	if *heuristic+*haiku+*sonnet+*opus == 0 {
		*heuristic = 8
	}
	agent.Models["opus"] = *opusModel

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	g, p, err := agent.FetchProblem(*server)
	if err != nil {
		log.Fatalf("fetch problem from %s: %v", *server, err)
	}
	log.Printf("problem %s: n=%d m=%d", p.Name, g.N, g.M)

	var client *anthropic.Client
	if *haiku+*sonnet+*opus > 0 {
		c := anthropic.NewClient()
		client = &c
		if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
			log.Printf("warning: ANTHROPIC_API_KEY is not set; LLM agents fall back to the heuristic policy if calls fail")
		}
	}

	rng := rand.New(rand.NewSource(*seed))
	var agents []*agent.Agent
	var wg sync.WaitGroup
	spawn := func(kind string, n int) {
		for i := 1; i <= n; i++ {
			name := fmt.Sprintf("%s%s-%02d", *prefix, kind, i)
			cfg := agent.Config{Server: *server, Name: name, Seed: rng.Int63(), Budget: *budget, Verbose: *verbose, Role: "worker"}
			var planner agent.Planner
			if kind != "heur" {
				cfg.Model = agent.Models[kind]
				if kind != "haiku" {
					cfg.Role = "strategist"
				}
				planner = agent.NewClaudePlanner(client, cfg.Model, 0.3+0.7*rng.Float64(), cfg.Role, g, p)
			}
			a := agent.New(cfg, planner)
			agents = append(agents, a)
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.Run(ctx, g, p)
			}()
			time.Sleep(20 * time.Millisecond) // stagger connects
		}
	}
	spawn("heur", *heuristic)
	spawn("haiku", *haiku)
	spawn("sonnet", *sonnet)
	spawn("opus", *opus)
	log.Printf("launched %d agents against %s", len(agents), *server)

	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pub, ver := 0, 0
				for _, a := range agents {
					p, v := a.Stats()
					pub += p
					ver += v
				}
				if st, err := fetchState(*server); err == nil {
					log.Printf("bounds %d <= chi <= %d | %d separations | swarm published %d, verified %d",
						st.Lower, st.Upper, len(st.Separations), pub, ver)
				}
			}
		}
	}()
	wg.Wait()
}

func fetchState(server string) (*state.View, error) {
	resp, err := http.Get(strings.TrimRight(server, "/") + "/state")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var v state.View
	return &v, json.NewDecoder(resp.Body).Decode(&v)
}
