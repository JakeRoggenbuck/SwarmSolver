import { h, s } from "../lib/dom";
import { svgRoot, travel } from "../lib/svgkit";
import type { Scene } from "../scene";
import { frame, reveal } from "../scene";

// Normalized layout: 0 = u, 1 = v.
const NODES: [number, number][] = [
  [0.08, 0.52], [0.92, 0.52], [0.32, 0.12], [0.68, 0.12], [0.5, 0.3],
  [0.3, 0.86], [0.7, 0.86], [0.16, 0.16], [0.84, 0.16], [0.5, 0.98],
];
const EDGES: [number, number][] = [
  [0, 2], [0, 4], [0, 5], [0, 7], [1, 3], [1, 4], [1, 6], [1, 8], [2, 3], [2, 4], [3, 4],
  [5, 6], [4, 5], [4, 6], [7, 2], [8, 3], [5, 9], [6, 9],
];
const S = new Set([0, 1, 2, 3, 4]);

const AGENTS = ["a03 · haiku", "a11 · haiku", "a19 · sonnet", "a24 · haiku", "a31 · opus", "a38 · haiku"];

type Box = { x: number; y: number; w: number; h: number };

function drawGraph(parent: Element, b: Box, r: number, labels: boolean) {
  const P = NODES.map(([nx, ny]) => ({ x: b.x + nx * b.w, y: b.y + ny * b.h }));
  const edges = s("g", { class: "sp-edges" }, parent);
  const edgeEls = EDGES.map(([a, c]) =>
    s("line", { x1: P[a]!.x, y1: P[a]!.y, x2: P[c]!.x, y2: P[c]!.y, "data-s": S.has(a) && S.has(c) ? "1" : "0" }, edges),
  );
  const newEdge = s(
    "line",
    { x1: P[0]!.x, y1: P[0]!.y, x2: P[1]!.x, y2: P[1]!.y, class: "sp-new", pathLength: 100 },
    parent,
  );
  const nodes = s("g", { class: "sp-nodes" }, parent);
  const nodeEls = P.map((p, i) =>
    s("circle", { cx: p.x, cy: p.y, r: i < 2 ? r * 1.35 : r, class: i < 2 ? "uv" : "", "data-s": S.has(i) ? "1" : "0" }, nodes),
  );
  if (labels) {
    s("text", { x: P[0]!.x, y: P[0]!.y + 8, "text-anchor": "middle", class: "sp-uv", text: "u" }, nodes);
    s("text", { x: P[1]!.x, y: P[1]!.y + 8, "text-anchor": "middle", class: "sp-uv", text: "v" }, nodes);
  }
  return { newEdge, edgeEls, nodeEls, center: { x: b.x + b.w / 2, y: b.y + b.h / 2 } };
}

export const separation: Scene = {
  id: "separation-edge",
  kicker: "How progress spreads",
  title: "A verified separation is a new edge",
  blurb:
    "When Z3 proves that u and v can never share a color in any k-coloring, every agent adds u–v as an edge to its working graph. Every future search gets easier, including other agents' Z3 calls. That is how one agent's progress becomes everyone's.",
  loop: 14000,
  mount(root, life) {
    const content = frame(root, this);
    const svg = svgRoot(1680, 690);
    svg.classList.add("sp-svg");
    content.append(svg);

    const main = drawGraph(svg, { x: 60, y: 90, w: 640, h: 360 }, 17, true);

    const tiles: { g: ReturnType<typeof drawGraph>; frame: SVGRectElement; tag: SVGTextElement }[] = [];
    AGENTS.forEach((name, i) => {
      const col = i % 3;
      const row = Math.floor(i / 3);
      const x = 900 + col * 265;
      const y = 10 + row * 275;
      const frameEl = s("rect", { x, y, width: 245, height: 255, rx: 16, class: "sp-tile" }, svg);
      s("text", { x: x + 18, y: y + 36, class: "sp-tile-name", text: name }, svg);
      const tag = s("text", { x: x + 18, y: y + 236, class: "sp-tile-tag", text: "working graph" }, svg);
      tiles.push({ g: drawGraph(svg, { x: x + 28, y: y + 62, w: 190, h: 140 }, 6, false), frame: frameEl, tag });
    });

    const packets = s("g", {}, svg);
    const lead = h("div", { class: "sp-chip lead", html: "<b>a17</b> proposes SEPARATION(u, v) · k=27 + witness S" });
    const z3 = h("div", { class: "sp-z3 mono", html: "<span>z3 ›</span> G[S] with c(u)=c(v), k colors → <b>unsat</b>" });
    const verified = h("div", { class: "sp-chip verified", html: "verified: <b>c(u) ≠ c(v)</b> → add edge u–v" });
    const caption = h("div", {
      class: "caption rv",
      html: "Every agent adds the edge. <b>Every future search gets easier</b>, including other agents' Z3 calls.",
    });
    content.append(lead, z3, verified, caption);

    life.after(700, () => lead.classList.add("in"));
    life.after(2000, () => {
      svg.classList.add("focus");
      z3.classList.add("in");
    });
    life.after(3500, () => {
      svg.classList.remove("focus");
      main.newEdge.classList.add("on");
      lead.classList.remove("in");
      verified.classList.add("in");
    });
    life.after(4800, () => {
      tiles.forEach((t, i) => {
        const a = main.center;
        const b = t.g.center;
        const path = s("path", { d: `M${a.x + 300},${a.y} Q${(a.x + b.x) / 2 + 150},${Math.min(a.y, b.y) - 60} ${b.x},${b.y}`, fill: "none" }, packets);
        life.after(i * 160, async () => {
          await travel(life, packets, path, "#3ddc97", 800, 8);
          t.g.newEdge.classList.add("on");
          t.frame.classList.add("hit");
          t.tag.textContent = "+ edge u–v";
          t.tag.classList.add("hit");
        });
      });
    });
    reveal(life, caption, 7400);
  },
};
