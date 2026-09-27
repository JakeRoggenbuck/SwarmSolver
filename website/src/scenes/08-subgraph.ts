import { h, s, ease, lerp } from "../lib/dom";
import { plantedGraph } from "../lib/graph";
import { svgRoot } from "../lib/svgkit";
import type { Scene } from "../scene";
import { frame, reveal } from "../scene";

const PANEL = { x: 1010, y: 10, w: 670, h: 450 };
const P_CENTER = { x: PANEL.x + PANEL.w / 2, y: PANEL.y + PANEL.h / 2 + 18 };

export const subgraph: Scene = {
  id: "subgraph-certificates",
  kicker: "Why the certificates are sound",
  title: "Z3 only sees a small piece",
  blurb:
    "Every constraint on the induced subgraph G[S] is also a constraint on G. So if G[S] can't be colored with k−1 colors, neither can G. Z3 only ever sees |S| vertices, about 20 to 60, so each check takes milliseconds or seconds even when the full graph is far too big to solve.",
  loop: 16000,
  mount(root, life) {
    const content = frame(root, this);
    const g = plantedGraph({
      seed: 21,
      cx: 430,
      cy: 290,
      rx: 410,
      ry: 280,
      n: 86,
      s: 14,
      sCenter: { x: 560, y: 250 },
      sRadius: 95,
      sDensity: 0.55,
    });
    const inS = new Set(g.S);

    const svg = svgRoot(1680, 690);
    svg.classList.add("sg-svg");
    content.append(svg);

    const panel = s("g", { class: "sg-panel" }, svg);
    s("rect", { x: PANEL.x, y: PANEL.y, width: PANEL.w, height: PANEL.h, rx: 20, fill: "#0e1219", stroke: "#b18cff", "stroke-width": 2 }, panel);
    s("text", { x: PANEL.x + 28, y: PANEL.y + 50, class: "sg-panel-title", text: "G[S]" }, panel);
    s("text", { x: PANEL.x + PANEL.w - 28, y: PANEL.y + 50, "text-anchor": "end", class: "sg-panel-sub", text: `${g.S.length} vertices → sent to Z3` }, panel);

    const bgEdges = s("g", { class: "sg-bg-edges" }, svg);
    const crossEdges = s("g", { class: "sg-cross" }, svg);
    const sEdges = s("g", { class: "sg-s-edges" }, svg);
    const bgNodes = s("g", { class: "sg-bg-nodes" }, svg);
    const sNodes = s("g", { class: "sg-s-nodes" }, svg);

    const pos = g.nodes.map((p) => ({ ...p }));
    const lines: { el: SVGLineElement; a: number; b: number }[] = [];
    for (const [a, b] of g.edges) {
      const both = inS.has(a) && inS.has(b);
      const one = inS.has(a) || inS.has(b);
      const el = s("line", { x1: pos[a]!.x, y1: pos[a]!.y, x2: pos[b]!.x, y2: pos[b]!.y }, both ? sEdges : one ? crossEdges : bgEdges);
      lines.push({ el, a, b });
    }
    const circles = pos.map((p, i) => s("circle", { cx: p.x, cy: p.y, r: inS.has(i) ? 11 : 8 }, inS.has(i) ? sNodes : bgNodes));

    s("text", { x: 560, y: 110, "text-anchor": "middle", class: "sg-s-label", text: `S ⊂ V · |S| = ${g.S.length}` }, svg);
    s("text", { x: 20, y: 30, class: "sg-g-label", text: `G · ${g.nodes.length} vertices shown (the real graph is far larger)` }, svg);

    const z3 = h(
      "div",
      { class: "sg-z3" },
      h("div", { class: "sg-z3-q mono", html: "<span>z3 ›</span> is G[S] (k−1)-colorable?" }),
      h("div", { class: "sg-z3-a mono", html: "<b>unsat</b> ⇒ χ(G[S]) ≥ k ⇒ <b>χ(G) ≥ k</b>" }),
    );
    const caption = h("div", {
      class: "caption sg-caption rv",
      html: "Every constraint on <b>G[S]</b> is also a constraint on <b>G</b>. &nbsp;Z3 sees <b>20–60 vertices</b>, not the whole graph.",
    });
    content.append(z3, caption);

    const target = new Map<number, { x: number; y: number }>();
    g.S.forEach((i, k) => {
      const a = -Math.PI / 2 + (k / g.S.length) * Math.PI * 2;
      target.set(i, { x: P_CENTER.x + Math.cos(a) * 165, y: P_CENTER.y + Math.sin(a) * 165 });
    });

    const redraw = () => {
      for (const { el, a, b } of lines) {
        el.setAttribute("x1", String(pos[a]!.x));
        el.setAttribute("y1", String(pos[a]!.y));
        el.setAttribute("x2", String(pos[b]!.x));
        el.setAttribute("y2", String(pos[b]!.y));
      }
      circles.forEach((c, i) => {
        c.setAttribute("cx", String(pos[i]!.x));
        c.setAttribute("cy", String(pos[i]!.y));
      });
    };

    svg.classList.add("stage-0");
    life.after(300, () => svg.classList.replace("stage-0", "stage-1"));
    life.after(2000, () => svg.classList.replace("stage-1", "stage-2"));
    life.after(3800, async () => {
      svg.classList.replace("stage-2", "stage-3");
      const from = new Map(g.S.map((i) => [i, { ...g.nodes[i]! }]));
      await life.tween(1700, (t) => {
        const e = ease.inOut(t);
        for (const i of g.S) {
          const a = from.get(i)!;
          const b = target.get(i)!;
          pos[i]!.x = lerp(a.x, b.x, e);
          pos[i]!.y = lerp(a.y, b.y, e);
        }
        redraw();
      });
      svg.classList.add("stage-4");
    });
    life.after(6200, () => z3.classList.add("q"));
    life.after(7400, () => z3.classList.add("a"));
    reveal(life, caption, 8600);
  },
};
