# Swarm: verified realtime communication for agent swarms

Hundreds of agents work on one problem at once. They share incremental findings over a
realtime log, and a Z3 harness checks each finding before any other agent builds on it.

**Demo:** tens of Claude agents attack the chromatic number of a hard DIMACS graph. A live
dashboard shows the gap between the best known coloring (upper bound) and the proven lower
bound closing as agents share verified findings.

## Decisions

| Area | Decision |
|---|---|
| Problem | Graph coloring (DIMACS instances, e.g. the DSJC family; check the current best known bounds before picking one) |
| Transport | WebSocket, one per agent |
| Verification | Two lanes: `leads` (unverified, instant) and `verified` (proven by the harness) |
| Delivery | Ordered log with global sequence numbers, replay from an offset, and a snapshot of best state |
| Server and agents | Go, in one repo |
| LLM | Mostly Haiku workers plus a few Sonnet/Opus strategists. Tens of real agents; synthetic clients for load testing |
| UI | Read-only web dashboard that subscribes to the WS stream |

## Claim types (graph coloring)

Every claim carries a small **certificate**, so checking it never means solving the whole problem.

| Kind | Claim | Certificate | Check |
|---|---|---|---|
| `COLORING` | chi(G) <= k | full coloring | O(E) edge scan, no Z3 needed |
| `CLIQUE` | chi(G) >= k | k vertices | check all pairs are edges, no Z3 needed |
| `SUBGRAPH_BOUND` | chi(G) >= k | vertex set S | Z3: G[S] is not (k-1)-colorable (unsat) |
| `SEPARATION` | in every k-coloring, c(u) != c(v) | vertex set S containing u, v | Z3: G[S] with c(u)=c(v) is not k-colorable (unsat) |
| `MERGE` | in some optimal coloring, c(u) = c(v) | (heuristic lead only) | stays on the `leads` lane unless it becomes part of a verified `COLORING` |

Why subgraph certificates are sound: every constraint on the induced subgraph G[S] is also a
constraint on G. So "G[S] can't do X" implies "G can't do X". Z3 only ever sees |S| vertices
(think 20-60), which keeps each check to milliseconds or seconds even when the full graph is
too big to solve.

A verified `SEPARATION` is effectively a new edge. Agents add it to their working graph, and
every future search, including other agents' Z3 calls, gets easier. This is how incremental
progress spreads through the swarm.

## Server architecture

```
agents --WS--> ingest chan --> [sequencer goroutine] --> ring []Envelope (2^16, prealloc)
                                 | assign seq, JSON-encode ONCE        | atomic head
                                 v                                     v
                       verifier pool (z3 -in -T:10)       per-client reader goroutines
                       VERDICT events --> ingest chan      cursor..head -> batched socket writes
                                 v
                       state view: best coloring, best lower bound,
                       verified separations (dedup by hash), leaderboard
```

- **Sequencer:** the only writer to the ring. It stamps `seq`, serializes once to `[]byte`, and
  publishes a new head with an atomic store. It then closes the current "generation channel"
  and replaces it with a new one, so a single close wakes every waiting reader.
- **Readers:** each client goroutine loops: read `head`, send `ring[cursor..head]` in one batched
  write, wait on the generation channel. The fan-out path takes no locks.
- **Slow clients:** if `head - cursor > len(ring)`, send the snapshot and jump the cursor to
  `head`. A slow client never blocks the sequencer.
- **Verifier pool:** N workers (about the number of CPU cores). Each runs `z3 -in` on the claim's
  SMT-LIB text with a timeout. Verdicts (`verified` / `refuted` with a model / `unknown`) are
  logged as events of their own. Results are cached by a hash of the normalized formula.
  Cheap checks (`COLORING`, `CLIQUE`) run in-process.
- **State view:** a single-goroutine fold over verified events. The sequencer publishes it as an
  immutable snapshot pointer via `atomic.Pointer`.
- **Optional persistence:** append encoded envelopes to a file so the server can replay after a
  restart.

### API

```
WS   /ws?from=<seq>&topics=leads,verified,verdicts,state   bidirectional; publish over the same socket
GET  /state                                                snapshot: best coloring, lower bound, separations
GET  /problem                                              graph (DIMACS) + metadata
GET  /metrics                                              msgs/sec, clients, verifier queue depth
```

### Envelope

```json
{ "seq": 1042, "topic": "leads", "agent": "a17", "ts": 1727460000123,
  "kind": "SEPARATION", "id": "sha256:...", "parents": ["sha256:..."],
  "claim": { "u": 12, "v": 88, "k": 27, "S": [12, 88, 3, 41] },
  "note": "<=200 chars rationale" }
```

`parents` records which verified findings a claim built on. This yields a provenance DAG, which
the dashboard can show as "which insight unlocked which".

## Agent loop (Go, anthropic-sdk-go)

1. Connect with `from=0&topics=verified,verdicts,state`, apply the snapshot, then keep a local
   working graph (the original edges plus verified separations).
2. Each turn, build a **digest**: problem summary (prompt-cached), current bounds, the agent's
   assigned focus region (a vertex neighborhood), recent separations touching that region, and
   the last few refutations with counterexamples.
3. The LLM picks a move: propose a dense subgraph for a bound, propose a separation with its
   witness set, or steer a local search (e.g. which color classes to recolor).
4. Run a local Go heuristic (DSatur or tabucol) seeded by the LLM's plan, then publish results.
5. Optionally run a quick local Z3 pre-check before publishing to `leads`, to save verifier
   capacity.

**No central coordinator.** Agents may overlap on the same regions, and that's intended. Each
agent hill-climbs from a different starting point, so overlap means the same area gets explored
several ways. Diversity comes from per-agent randomness, not from assigned work:

- a random seed for the local search and a random starting coloring
- a randomly chosen strategy prompt (e.g. "attack dense cores", "recolor the largest color
  class", "look for separations near high-degree vertices")
- a random focus region, re-drawn every few turns
- a mix of models and sampling temperatures

Overlap is cheap for the server. When two agents find the same claim, it hashes to the same
`id`, so it is checked by the verifier once and the second copy is dropped. Only claims that are
actually different cost verifier time.

## Build plan (about 1.5 days)

1. **Server core** (ring, sequencer, WS readers, replay). Write a synthetic load client early
   and measure fan-out at 500 clients.
2. **Graph + cheap checkers:** DIMACS parser, `COLORING` and `CLIQUE` validation, state view.
3. **Z3 harness:** SMT-LIB generator for k-coloring of G[S] (+ c(u)=c(v)), worker pool, cache.
4. **Agent:** a heuristic-only agent first (no LLM), then add the Claude planning loop.
5. **Dashboard:** gap chart (upper vs lower over time), msgs/sec, verified/refuted/unknown
   counts, per-agent contributions.
6. **Demo script:** run Z3 alone on the full graph (it times out), then the swarm (bounds move).

## Open questions

- Which instance? It needs a real gap between known bounds, and small witness subgraphs must
  exist for the lower bound.
- ~~Central work assignment?~~ Decided: none. Agents choose their own focus at random and
  overlap is expected.
- Should verified lemmas expire or be ranked, so the digest keeps only the most useful ones?
- How do you give agents credit or incentives? (Leaderboard by verified contributions?)
