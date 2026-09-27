# Swarm: verified realtime communication for agent swarms

Tens to hundreds of agents work on one hard problem at once. They share incremental findings
over a realtime log, and a Z3 harness checks every finding before any other agent builds on it.
Agents never trust a compelling-sounding lead: they build only on claims that come with a
certificate the harness has verified.

**The demo problem** is the chromatic number of DIMACS graphs. A live dashboard shows the gap
between the best verified coloring (upper bound) and the best verified lower-bound certificate
closing as agents share findings.

```
DSJC125.5 (n=125, m=3891), 12 heuristic agents on a laptop:
  t=0s    2 <= chi <= 125
  t=1s   10 <= chi <= 17    DSatur -> tabu search reaches the literature-best coloring; clique of 10
  t=16s  13 <= chi <= 17    Z3 proves 57-vertex subgraphs are not 12-colorable

Z3 alone on the full graph, same question (chi >= 13): no answer after 30s.
```

## Quickstart

```bash
brew install z3            # optional: the server falls back to a native solver
make build
./bin/swarm-server -graph instances/dsjc125.5.col      # dashboard at http://localhost:8080
./bin/swarm-agent -heuristic 16                          # no API key needed
ANTHROPIC_API_KEY=... ./bin/swarm-agent -haiku 16 -sonnet 3 -opus 1 -heuristic 4
```

Or run the whole demo (Z3 baseline, server, dashboard, swarm): `scripts/demo.sh`.

| Command | What it does |
|---|---|
| `swarm-server` | The realtime log, verifier pool, state view, HTTP API, and embedded dashboard |
| `swarm-agent` | Launches N agents in one process: heuristic, Haiku workers, Sonnet/Opus strategists |
| `swarm-solo` | Baseline: gives the whole graph to Z3 and asks it to prove `chi >= k` |
| `swarm-load` | Synthetic load: N subscribers and P publishers; reports fan-out and latency |
| `swarm-tail` | Prints the event log readably (`-from 1 -topics verdicts` replays every verdict) |

## Claim types

Every claim carries a small certificate, so checking it never means solving the whole problem.

| Kind | Claim | Certificate | Check |
|---|---|---|---|
| `COLORING` | chi(G) <= k | full coloring | O(E) edge scan |
| `CLIQUE` | chi(G) >= k | k vertices | pairwise adjacency scan |
| `SUBGRAPH_BOUND` | chi(G) >= k | vertex set S | Z3: G[S] + separation edges is not (k-1)-colorable |
| `SEPARATION` | every k-coloring has c(u) != c(v) | vertex set S containing u, v | Z3: G[S] with u,v contracted is not k-colorable |
| `MERGE` | some good coloring has c(u) = c(v) | none | lead only, never verified |
| `NOTE` | free text (strategist advice, load traffic) | none | lead only |

**Why subgraph certificates are sound.** Every constraint on G[S] is also a constraint on G, so
"G[S] can't be colored with k-1 colors" implies "G can't". Z3 only sees |S| vertices.

**Why separations compose.** A verified separation at level k holds for every k' <= k (a
k'-coloring is also a k-coloring). So it acts as a new edge for any later proof at level <= k:

- a `SUBGRAPH_BOUND` for chi >= k may use separations proven at level >= k-1;
- a `SEPARATION` at level k may use separations proven at level >= k.

The server enforces these levels. It also attaches every applicable verified separation inside
S automatically, and records them as the claim's `parents`. That is the provenance DAG the
dashboard draws, and it is how an insight from one agent shortens another agent's proof.

## Architecture

```
agents --WS--> ingest chan --> [sequencer goroutine] --> ring []*Entry (2^16, atomic slots)
                                 | assign seq, JSON-encode ONCE        | atomic head
                                 | dedup by content address            | generation channel
                                 v                                     v
                       verifier pool (z3 -in -T:10)       per-client reader goroutines
                       fast lane: COLORING/CLIQUE          cursor..head -> one batched write
                       VERDICT events --> ingest chan      lapped? -> snapshot + jump
                                 v
                       state fold (sequencer goroutine) -> atomic.Pointer[View]
                       best coloring, lower bound + witness, separations, leaderboard
```

- **Sequencer** (`internal/server/server.go`): the only writer to the ring and the only owner of
  the state fold. It drains up to 4096 queued messages per wake-up, stamps `seq`, serializes each
  envelope once, stores it in its ring slot, then publishes the new head with an atomic store
  and closes the current generation channel. One `close` wakes every waiting reader.
- **Readers** (`internal/server/http.go`): each client goroutine loads the generation channel,
  reads `head`, copies `ring[cursor..head]` (filtered by a topic bitmask) into one NDJSON frame,
  writes it, and waits. The fan-out path takes no locks.
- **Slow clients**: ring slots are `atomic.Pointer`s tagged with their seq. A reader that finds a
  slot overwritten (it was lapped) gets the current snapshot and jumps to the head. A slow
  client never blocks the sequencer.
- **Verifier pool** (`internal/verify`): `NumCPU` workers pipe SMT-LIB into `z3 -in -T:<s>`.
  COLORING and CLIQUE run in-process on their own lane so they never queue behind a 10-second Z3
  call. Before any subgraph check, the harness peels G[S] to its k-core: vertices with degree < k
  can always be colored last, so only the core reaches Z3. A clique inside the core is pinned to
  colors 0..q-1 to break symmetry. Results are cached by a hash of the normalized formula. Claims
  that became stale while queued (a better bound already landed) are skipped.
- **Refutations carry counterexamples**: a sat result is extended back to a coloring of all of
  G[S] (greedy in reverse k-core removal order always succeeds), and returned in the verdict.
- **Dedup**: claims are normalized (sets sorted, u < v, colorings relabeled by first appearance)
  and content-addressed (`sha256:...`). Two agents that find the same claim cost one check.
- **Persistence**: `-log events.ndjson` appends every encoded envelope. On restart the server
  replays it into the ring and the fold.

### API

```
WS   /ws?from=<seq>&topics=leads,verified,verdicts,state&agent=<name>&model=<model>
       Without from: a snapshot, then everything after it. With from: replay from that seq
       (a snapshot first if it was evicted). Publish by sending envelopes (NDJSON) on the socket.
GET  /state                  snapshot: bounds, best coloring, witness, separations, leaderboard, history
GET  /problem[?format=dimacs] graph + metadata
GET  /metrics                rates, clients, ingest queue, verifier queue/busy/z3 calls/cache hits, resyncs
POST /publish?agent=<name>   publish envelopes over HTTP (NDJSON body), handy for curl
```

### Envelope

```json
{ "seq": 1042, "topic": "leads", "agent": "haiku-07", "ts": 1727460000123,
  "kind": "SEPARATION", "id": "sha256:150c7eae3f66c9298b9c0ed3", "parents": ["sha256:..."],
  "claim": { "k": 12, "u": 55, "v": 78, "S": [3, 12, 41, 55, 78] },
  "note": "c(55)=c(78) forces a non-12-colorable 48-vertex subgraph" }
```

Verdicts are their own events: `{"topic":"verdicts","kind":"VERDICT","verdict":{"of":"sha256:...",
"status":"refuted","engine":"z3","ms":127,"core":60,"reason":"found a 13-coloring ...","counter":[[v,c],...]}}`.

## Agents

Each agent (`internal/agent`) connects with `from=1`, replays the verified history into a local
view (bounds, best coloring, witness, separations), then loops:

1. **Digest**: bounds, class sizes of the best coloring, the current witness, a random focus
   region (re-drawn every few turns), nearby separations, recent refutations with reasons, other
   agents' leads, and its own recent move outcomes.
2. **Plan**: Claude picks one move as JSON (`internal/agent/planner.go`). The problem summary and
   move menu are a cached system prompt. Haiku workers get a per-agent random temperature; Sonnet
   and Opus strategists run at low effort and may `broadcast` advice that workers see as leads.
   Heuristic agents (or LLM agents whose calls fail 3 times) use a weighted random policy.
3. **Execute** within a compute budget (default 4s), all seeded by the plan:
   - `tabucol`: drop a color class from the swarm's best coloring and repair with tabu search,
     on G plus separations valid at that level. Publishes `COLORING`.
   - `bound`: grow a subgraph from a clique (or the current witness) until the native DSatur
     branch-and-bound proves it is not (k-1)-colorable, then minimize it. Growth is
     **counterexample-guided**: each sat check yields a coloring, and the next vertex added is the
     one whose neighbors in S already see the most distinct colors. If the local solver can't
     decide, the agent sometimes publishes anyway and lets the Z3 harness settle it.
   - `separate`: pick non-adjacent u, v whose joint neighborhood hosts a large clique, seed S with
     that clique, and grow until the contraction u=v is not L-colorable (L = current lower bound).
   - `clique`, `merge`: clique search; MERGE leads from the best coloring.

There is no central coordinator. Diversity comes from per-agent seeds, a random strategy prompt,
random focus regions, and a mix of models and temperatures. Overlap is cheap for the server,
because identical claims hash to the same id.

## Measured

**Fan-out** (`swarm-load`, Apple Silicon laptop, 6 cores, load generator on the same machine):

| subscribers | publish rate | delivered | p50 / p99 latency | resyncs |
|---|---|---|---|---|
| 500 | 1,000 msg/s | 499k/s | 3.9 / 10.9 ms | 0 |
| 500 | 3,400 msg/s | 1.71M/s | 42 / 257 ms | 0 |

Batching does its job at high load: frames average 8-50 envelopes, so the per-socket write cost
is paid once per batch rather than once per event.

**Verification** (DSJC125.5): k-core-pruned Z3 checks take 40-80 ms for chi >= 11/12 certificates
on 28-42 vertices and 2-7 s for chi >= 13 on 56-57 vertices. Z3 on the full 125-vertex graph does
not decide chi >= 13 within 30 s. It does decide chi >= 11 quickly (196 ms), since a pinned
10-clique makes that bound easy.

## Instances

`instances/` holds DIMACS graphs. Literature best colorings are shown on the dashboard as a
dashed reference line.

| instance | n | notes |
|---|---|---|
| `dsjc125.5` | 125 | default. 16 heuristic agents: 13 <= chi <= 17 in ~16 s (17 = literature best) |
| `dsjc250.5` | 250 | 14 <= chi <= 28 in ~8 s (28 = literature best); Z3 alone can't decide chi >= 14 in 20 s |
| `dsjc500.5` | 500 | best live demo: both bounds keep moving. 15 <= chi <= 49 after 3 min (literature best 47) |
| `myciel5..7` | 47-191 | triangle-free: clique bound is 2 but chi is 6-8, so every lower bound step needs Z3 |
| `queen8_8`, `queen9_9`, `le450_*`, `dsjc125.1/.9` | | more classics |

## Changes from design.md

- **Verdict status `skipped`** for claims that can no longer improve a bound (checked at ingest
  and again when a worker picks the job up). This saves solver time when many agents overlap.
- **`NOTE` kind** for free-text leads. Strategist broadcasts and load-test traffic use it.
- **Separation levels are explicit and enforced**, and applicable separations are auto-attached as
  parents (see *Why separations compose*).
- **k-core pruning** before Z3 and **full counterexample colorings** on refutation.
- The Opus strategist model defaults to `claude-opus-5` (override with `-opus-model`).

## Layout

```
cmd/swarm-server  cmd/swarm-agent  cmd/swarm-solo  cmd/swarm-load  cmd/swarm-tail
internal/bus      ring buffer, atomic head, generation-channel wakeups
internal/server   sequencer, dedup, parents, state publishing, WS readers, HTTP API, replay
internal/verify   certificate checks, verifier pool, formula cache
internal/smt      SMT-LIB generation and the z3 runner
internal/graph    DIMACS, bitsets, induced subgraphs + contraction, k-core, DSatur,
                  exact DSatur branch-and-bound, tabucol, clique search
internal/state    fold over verified events -> immutable View snapshots
internal/agent    agent loop, digest, move executors, Claude planner
internal/proto    envelope, claim kinds, normalization, content addressing
web/static        dashboard (single file, no dependencies, embedded in the server binary)
```

`make test` runs the suite with the race detector. It covers solver soundness on Mycielski graphs
(both Z3 and native), separations that turn paths into triangles, content-address stability,
the full publish -> verdict -> verified -> bounds flow over real WebSockets, dedup, stale skips,
parent resolution, evicted-replay snapshots, log replay after restart, and the Claude request
shape against a mock Messages API.
