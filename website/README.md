# Swarm website

A Bun site that explains how Swarm works, built from `../DESIGN.md`. Every diagram is a
live, animated 1920×1080 scene, so it can be screen-recorded straight into a video.

```sh
bun install
bun run dev        # http://localhost:3000, with hot reload
bun run build      # static bundle in dist/
bun run typecheck
```

## Scenes

| # | id | What it shows |
|---|---|---|
| 1 | `title` | Title card with a drifting agent network |
| 2 | `divide` | Labs vs. individuals |
| 3 | `missing` | The two missing pieces: verify + share |
| 4 | `architecture` | Agents → ingest → sequencer → ring → readers, plus the Z3 verifier loop and state view |
| 5 | `ring-buffer` | Single-writer ring, generation-channel wakeups, batched readers, slow-client snapshot |
| 6 | `two-lanes` | Leads lane → Z3 harness → verified / refuted / unknown |
| 7 | `claim-types` | The five claim kinds and their certificates |
| 8 | `subgraph-certificates` | Why checking G[S] is sound, and why it's small |
| 9 | `separation-edge` | A verified separation becomes an edge in every agent's graph |
| 10 | `envelope-provenance` | The JSON envelope and the provenance DAG from `parents` |
| 11 | `agent-loop` | Sync → digest → plan → search → publish |
| 12 | `no-coordinator` | Random diversity per agent; hash dedup makes overlap cheap (live sim) |
| 13 | `dashboard` | Upper vs. lower bound gap chart (simulated data) |
| 14 | `z3-vs-swarm` | The demo script: Z3 alone times out, the swarm moves the bounds (illustrative) |
| 15 | `closing` | Summary card |

## Recording

- Open `/#present/<id>` (or click any scene). `←`/`→`/`Space` switch scenes, `R` replays, `F` goes full screen, `Esc` exits.
- Controls and cursor fade after 2 s without mouse movement. `H` or `?clean` hides them for good.
- The stage is always 1920×1080 and letterboxes to fit. Record a 1080p or 4K screen for a pixel-exact frame.
- Layouts use fixed seeds, so every take looks the same. Scenes with one-shot timelines loop on their own after a hold.

## Layout

```
server.ts            Bun.serve with an HTML import
index.html           entry
src/main.ts          landing page, present mode, stage scaling
src/scene.ts         Scene type and shared header/frame
src/scenes/*.ts      one file per scene; order lives in scenes/index.ts
src/scenes.css       per-scene styles
src/lib/             DOM/SVG helpers, lifecycle (timers + tweens), graph + sim generators
```

The colors mean the same thing everywhere: amber = lead / upper bound, green = verified,
red = refuted, blue = transport / lower bound, violet = Z3.
