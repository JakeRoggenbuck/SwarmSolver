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
| `swarm-gen` | Generates a new, never-published instance with a planted answer (see [New problems](#new-problems-generated-instances-with-a-known-answer)) |
| `swarm-gen` | Generates a fresh instance with a planted answer (see *Fresh instances*) |

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
- **Restarts**: a client that reconnects with a `from` beyond the current head (it saw a previous
  run) gets a snapshot and resyncs, just like a lapped reader. The dashboard re-reads `/state`
  before reconnecting and, if `started_at` changed, drops the old run's feed and reloads the problem.

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
| `queen8_8`, `queen9_9`, `le450_*`, `dsjc125.1/.9` | | more classics. `le450_15c` (chi = 15) stalled at 16 colors for 3 min, even with 45 s searches |
| `generated/swarm250_25_a`, `_b` | 250 | new instances with a planted answer, chi = 25; solved by the swarm in 31 s and 86 s (see [New problems](#new-problems-generated-instances-with-a-known-answer)) |
| `dsjc1000.5`, `C2000.5`, `flat1000_76_0`, `latin_square_10` | 900-2000 | open benchmarks: chi is not settled, so the swarm is working on unsolved problems |
| `generated/swarm250_25_a` | 250 | fresh `swarm-gen` instance, never published: chi = 25 by construction |

### Fresh instances

Published benchmarks may already be in a model's training data. `swarm-gen` builds new instances
that nobody has attempted, but whose answer is still known:

```bash
./bin/swarm-gen -n 250 -k 20 -p 0.42 -seed 7                 # writes instances/generated/planted250_20_s7.col
./bin/swarm-gen -n 250 -k 20 -p 0.42 -seed 7 -calibrate 2m   # time parallel tabu search on it instead
```

It splits the vertices into k shuffled hidden color classes, adds "flat" random edges between
classes (every pair of classes gets the same edge count, so degrees give nothing away), and plants
one k-clique. The hidden classes prove chi <= k and the clique proves chi >= k. The density `p`
sets the difficulty: near the density where a random graph would need about k colors on its own,
the hidden coloring is hard to find. `-calibrate` reports how long tabu search needs, so you can
pick a `p` that is hard but reachable.

The `.col` file carries the answer in a comment (`c known: lower=K upper=K`). `swarm-server`
reads it and the dashboard shows it as the "known answer (planted)" reference line instead of a
literature best. The hidden coloring goes to `solutions/<name>.sol`, which agents never see.

## New problems: generated instances with a known answer

The published instances turned out to be either too easy for the swarm or genuinely open, so
neither measures much. `swarm-gen` makes fresh instances nobody has seen before, each with an
answer we know by construction:

1. Split n vertices into k hidden color classes of equal size (vertex ids are shuffled).
2. Add random edges only between different classes. Every pair of classes gets about the same
   number of edges ("flat"), so vertex degrees give nothing away.
3. Plant a k-clique by picking one vertex from each class and connecting them.

The hidden classes prove chi <= k and the clique proves chi >= k, so **chi = k exactly**. A new
seed gives a new graph that has never been published or attempted.

```bash
./bin/swarm-gen -n 250 -k 25 -p 0.506 -seed 2 -name swarm250_25_a   # writes the instance + hidden solution
./bin/swarm-gen -n 250 -k 25 -p 0.506 -seed 2 -calibrate 4m          # time 6 parallel tabu searches first
./bin/swarm-server -graph instances/generated/swarm250_25_a.col
./bin/swarm-agent -heuristic 12 -budget 30s
```

The instance goes to `instances/generated/<name>.col`, and its hidden coloring to
`instances/generated/solutions/<name>.sol`. Agents never see the solution file, because they
download the graph from `/problem`. The `.col` header carries a line `c known: lower=25 upper=25`.
The server reads it and the dashboard draws it as the "Known answer (planted)" target line.
Agents are told the target number, the same way they are told the best known coloring for
published instances, but not the coloring itself.

### Tuning difficulty

Difficulty comes from how close k is to the number of colors a random graph of that density
would need anyway. For 250 vertices at edge density about 0.5 that natural number is about 28
(DSJC250.5 has the same size and density, and its best known coloring uses 28). Measured with
`-calibrate` (6 parallel tabu searches, fresh start every 2M iterations, seed 1 unless noted):

| n | k | p (between classes) | time to find the hidden k-coloring |
|---|---|---|---|
| 250 | 20 | 0.30 / 0.36 / 0.42 | 0s / 0.2s / 0.1s. Far below 28, the hidden coloring stands out |
| 250 | 24 | 0.50 | 0.4s |
| 250 | 25 | 0.50 | not solved in 4 min (best: 15 conflicting edges) |
| 250 | 26 | 0.50 | not solved in 4 min (best: 6 conflicting edges) |
| 250 | 27 | 0.50 | not solved in 1 min (best: 2 conflicting edges) |
| 250 | 28 | 0.50 | 7.8s. At the natural color count many valid colorings exist, so one is easy to find |
| 250 | 25 | 0.53 / 0.56 | 0.2s / 0s |

The hard band is narrow: k = 25–27 at density 0.5, and for k = 25 only p between about 0.50 and
0.51. Inside it, seed and luck matter as much as p:

| k = 25, p | seed 1 | seed 2 | seed 3 |
|---|---|---|---|
| 0.503 | 36s | not solved in 4 min | not solved in 4 min |
| 0.506 | 18s | 1m51s | not solved in 4 min |

The same graph can take 0.7s in one run and 35.5s in another. Solve time is a first-hit time,
so one run says little. Compare settings over several seeds and runs.

Two things caught while tuning:

- **Density was coarsely quantized.** With 10 vertices per class there are 100 possible edges
  between two classes, and rounding made 0.505 and 0.51 produce the identical graph. The
  generator now rounds up or down at random per pair of classes, so p varies smoothly.
- **The swarm is not the same as the calibration.** The calibration restarts tabu search from
  scratch; swarm agents start from the shared best (k+1)-coloring and drop one color class.

### Result: swarm250_25_a

`swarm250_25_a` (n=250, m=15,320, k=25, p=0.506, seed 2) is new: it was generated for this
test and has never been published. Its answer is chi = 25 by construction. **The swarm solved
it: 12 heuristic agents (no LLM, `-budget 30s`) proved chi = 25 in 31 seconds.**

| time after start | bounds | how |
|---|---|---|
| 1s | 2 <= chi <= 37 | first DSatur colorings |
| 1s | 25 <= chi <= 35 | clique search finds the planted 25-clique |
| 1s | 25 <= chi <= 28 | tabu search, several agents |
| 3s | 25 <= chi <= 27 | heur-06 |
| 31s | 25 <= chi <= 26 | heur-05 |
| 31s | **25 <= chi <= 25** | heur-07 finds a 25-coloring; the edge-scan check verifies it; gap 0 |

**Is it an easy problem?** For the swarm, fairly easy: 31 seconds, well short of the 10-minute
target. It still sits in the hard band. DSatur alone needs 36 colors, and one calibration run of
6 tabu searches took 1m51s. With one run each, "the swarm was faster" is an observation, not a
measured speedup.

**What it does and doesn't show:**
- The lower bound is easy on purpose: the planted 25-clique is found within a second. The work
  is all on the upper side, going from 28 to 25 colors.
- This is a benchmark, not a discovery. We built the answer in, so solving it demonstrates the
  swarm can find a hidden 25-coloring nobody has attempted. It adds no new mathematics.
- Unknown: whether the 25-coloring found is the planted one or a different valid coloring. The
  run wasn't recorded with `-log`, so the final coloring wasn't kept. To check next time, run
  the server with `-log`, then compare `best_coloring` from `/state` with the `.sol` file.

### Result: swarm250_25_b (harder)

`swarm250_25_b` (n=250, m=15,226, k=25, p=0.503, seed 2) is also new. On this exact graph, the
6-search calibration did **not** find the 25-coloring in 4 minutes (best: 13 conflicting edges).
**The swarm solved it: same setup (12 heuristic agents, `-budget 30s`), chi = 25 proven in
86 seconds.**

| time after start | bounds | how |
|---|---|---|
| 1s | 25 <= chi <= 28 | planted clique found; tabu search from DSatur colorings |
| 23s | 25 <= chi <= 27 | |
| 86s | 25 <= chi <= 26 | heur-07 |
| 86s | **25 <= chi <= 25** | heur-07 again, the same second: dropped a class from its own 26-coloring and repaired it |

**Easy or hard?** Harder than `_a` (86s vs 31s) and still solved, but still short of the
10-minute target. This is the one case where the swarm beat the calibration on the same graph.
A plausible reason is the start: swarm agents begin from the shared best 26-coloring and drop
one class, while the calibration restarts from scratch. That's one run each, so it's a lead
worth testing, not a measured result. As with `_a`, it's unknown whether the coloring found is
the planted one, because the run wasn't recorded with `-log`.

### Summary

| instance | new? | known answer | calibration (6 tabu searches) | swarm (12 agents) | solved? |
|---|---|---|---|---|---|
| `swarm250_25_a` | yes | chi = 25 | 1m51s | 31s | yes, gap 0 |
| `swarm250_25_b` | yes | chi = 25 | not solved in 4 min | 86s | yes, gap 0 |

Both are fresh problems that no one had attempted, both are genuinely hard for a greedy
algorithm (DSatur needs 35+ colors), and the swarm solved both in under 90 seconds. To reach the
10-minute target, try other seeds at p = 0.500–0.503, or a larger graph (for example n = 300
with k near its natural color count), and rerun with `-log` so the result can be compared with
the planted solution:

```bash
./bin/swarm-gen -n 250 -k 25 -p 0.500 -seed 3 -calibrate 4m    # check difficulty first
./bin/swarm-gen -n 250 -k 25 -p 0.500 -seed 3 -name swarm250_25_c
./bin/swarm-server -graph instances/generated/swarm250_25_c.col -log swarm250_25_c.ndjson
./bin/swarm-agent -heuristic 12 -budget 30s
```

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
cmd/swarm-server  cmd/swarm-agent  cmd/swarm-solo  cmd/swarm-load  cmd/swarm-tail  cmd/swarm-gen  cmd/swarm-gen
internal/bus      ring buffer, atomic head, generation-channel wakeups
internal/server   sequencer, dedup, parents, state publishing, WS readers, HTTP API, replay
internal/verify   certificate checks, verifier pool, formula cache
internal/smt      SMT-LIB generation and the z3 runner
internal/graph    DIMACS (with comments), bitsets, induced subgraphs + contraction, k-core, DSatur,
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
