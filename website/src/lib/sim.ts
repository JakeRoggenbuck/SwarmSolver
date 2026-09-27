import { rng } from "./dom";

/**
 * A simulated run for the dashboard scenes. These are NOT measured results:
 * the scenes that use them say so on screen.
 */
export const SIM = { minutes: 60, upper0: 58, upperEnd: 49, lower0: 13, lowerEnd: 24 };

export type BoundEvent = { t: number; value: number; kind: string; agent: string };
export type FeedEvent = { t: number; seq: number; agent: string; kind: string; text: string; verdict: "verified" | "refuted" | "unknown" };

export function simulate(seed = 4) {
  const r = rng(seed);
  const agent = () => `a${String(1 + Math.floor(r() * 48)).padStart(2, "0")}`;

  const steps = (from: number, to: number, early: number) => {
    const n = Math.abs(to - from);
    // front-load the improvements a little: easy wins come first
    const ts = Array.from({ length: n }, () => Math.pow(r(), early) * SIM.minutes * 0.95 + 0.5).sort((a, b) => a - b);
    const dir = Math.sign(to - from);
    return ts.map((t, i) => ({ t, value: from + dir * (i + 1) }));
  };

  const upper: BoundEvent[] = steps(SIM.upper0, SIM.upperEnd, 1.6).map((e) => ({ ...e, kind: "COLORING", agent: agent() }));
  const lower: BoundEvent[] = steps(SIM.lower0, SIM.lowerEnd, 1.2).map((e, i) => ({
    ...e,
    kind: i === 0 ? "CLIQUE" : "SUBGRAPH_BOUND",
    agent: agent(),
  }));

  const feed: FeedEvent[] = [];
  let seq = 900;
  for (const e of upper) feed.push({ t: e.t, seq: 0, agent: e.agent, kind: e.kind, text: `χ ≤ ${e.value}`, verdict: "verified" });
  for (const e of lower) feed.push({ t: e.t, seq: 0, agent: e.agent, kind: e.kind, text: `χ ≥ ${e.value}`, verdict: "verified" });
  for (let i = 0; i < 90; i++) {
    const t = r() * SIM.minutes;
    const roll = r();
    const u = Math.floor(r() * 500);
    const v = Math.floor(r() * 500);
    feed.push({
      t,
      seq: 0,
      agent: agent(),
      kind: "SEPARATION",
      text: `c(${u}) ≠ c(${v})`,
      verdict: roll < 0.6 ? "verified" : roll < 0.87 ? "refuted" : "unknown",
    });
  }
  feed.sort((a, b) => a.t - b.t);
  for (const f of feed) f.seq = seq += 3 + Math.floor(r() * 40);

  const at = (list: BoundEvent[], t: number, initial: number) => {
    let v = initial;
    for (const e of list) if (e.t <= t) v = e.value;
    return v;
  };

  return {
    upper,
    lower,
    feed,
    upperAt: (t: number) => at(upper, t, SIM.upper0),
    lowerAt: (t: number) => at(lower, t, SIM.lower0),
  };
}
