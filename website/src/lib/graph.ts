import { rng } from "./dom";

export type Pt = { x: number; y: number };
export type Graph = { nodes: Pt[]; edges: [number, number][]; S: number[] };

/**
 * A random "background" graph inside an ellipse with a planted dense cluster S.
 * S is the witness subgraph the scenes zoom into.
 */
export function plantedGraph(opts: {
  seed: number;
  cx: number;
  cy: number;
  rx: number;
  ry: number;
  n: number;
  s: number;
  sCenter: Pt;
  sRadius: number;
  sDensity: number;
}): Graph {
  const r = rng(opts.seed);
  const nodes: Pt[] = [];
  const S: number[] = [];

  // S first, on a sunflower spiral so it reads as one tight cluster
  for (let i = 0; i < opts.s; i++) {
    const rad = opts.sRadius * Math.sqrt((i + 0.5) / opts.s);
    const a = i * 2.39996;
    nodes.push({ x: opts.sCenter.x + rad * Math.cos(a), y: opts.sCenter.y + rad * Math.sin(a) });
    S.push(i);
  }

  let tries = 0;
  while (nodes.length < opts.s + opts.n && tries++ < 20000) {
    const a = r() * Math.PI * 2;
    const d = Math.sqrt(r());
    const p = { x: opts.cx + Math.cos(a) * opts.rx * d, y: opts.cy + Math.sin(a) * opts.ry * d };
    if (Math.hypot(p.x - opts.sCenter.x, p.y - opts.sCenter.y) < opts.sRadius + 45) continue;
    if (nodes.some((q) => Math.hypot(q.x - p.x, q.y - p.y) < 46)) continue;
    nodes.push(p);
  }

  const key = (a: number, b: number) => (a < b ? `${a}-${b}` : `${b}-${a}`);
  const seen = new Set<string>();
  const edges: [number, number][] = [];
  const add = (a: number, b: number) => {
    if (a === b || seen.has(key(a, b))) return;
    seen.add(key(a, b));
    edges.push([a, b]);
  };

  for (let i = 0; i < opts.s; i++)
    for (let j = i + 1; j < opts.s; j++) if (r() < opts.sDensity) add(i, j);

  for (let i = 0; i < nodes.length; i++) {
    const near = nodes
      .map((q, j) => ({ j, d: Math.hypot(q.x - nodes[i]!.x, q.y - nodes[i]!.y) }))
      .filter((o) => o.j !== i && (i >= opts.s || o.j >= opts.s))
      .sort((a, b) => a.d - b.d)
      .slice(0, i < opts.s ? 1 : 3);
    for (const o of near) add(i, o.j);
  }
  return { nodes, edges, S };
}
