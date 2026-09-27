import { h, s } from "../lib/dom";
import { SIM, simulate, type BoundEvent } from "../lib/sim";
import { svgRoot } from "../lib/svgkit";
import type { Scene } from "../scene";
import { frame } from "../scene";

const CH = { x: 0, y: 170, w: 1090, h: 500 };
const PLOT = { l: 70, r: 330, t: 60, b: 60 }; // right margin holds the direct labels
const K_MIN = 10;
const K_MAX = 60;
const RUN_MS = 22000;

export const dashboard: Scene = {
  id: "dashboard",
  kicker: "The demo",
  title: "Watching the gap close",
  blurb:
    "The dashboard is a read-only WebSocket subscriber. It plots the best known coloring (the upper bound) against the best proven lower bound over time. Every step in either line is a verified finding from some agent. The numbers in this scene are simulated.",
  loop: RUN_MS + 6000,
  mount(root, life) {
    const content = frame(root, this);
    const sim = simulate();

    // ── stat tiles
    const tiles = h("div", { class: "db-tiles" });
    const tile = (label: string, cls = "") => {
      const v = h("b", { class: "mono" });
      tiles.append(h("div", { class: `db-tile ${cls}` }, h("span", { text: label }), v));
      return v;
    };
    const tUpper = tile("upper bound", "upper");
    const tLower = tile("lower bound", "lower");
    const tGap = tile("gap");
    const tMsgs = tile("msgs / sec");
    const tAgents = tile("agents connected");
    const tQueue = tile("verifier queue");
    content.append(tiles);

    // ── gap chart
    const svg = svgRoot(CH.w, CH.h);
    const chartWrap = h("div", { class: "db-chart" }, svg);
    content.append(chartWrap);
    const pw = CH.w - PLOT.l - PLOT.r;
    const ph = CH.h - PLOT.t - PLOT.b;
    const X = (t: number) => PLOT.l + (t / SIM.minutes) * pw;
    const Y = (k: number) => PLOT.t + (1 - (k - K_MIN) / (K_MAX - K_MIN)) * ph;

    const grid = s("g", { class: "db-grid" }, svg);
    for (let k = K_MIN; k <= K_MAX; k += 10) {
      s("line", { x1: PLOT.l, x2: PLOT.l + pw, y1: Y(k), y2: Y(k) }, grid);
      s("text", { x: PLOT.l - 14, y: Y(k) + 7, "text-anchor": "end", text: String(k) }, grid);
    }
    for (let m = 0; m <= SIM.minutes; m += 10)
      s("text", { x: X(m), y: PLOT.t + ph + 38, "text-anchor": "middle", text: `${m}m` }, grid);
    s("text", { x: 24, y: 34, class: "db-axis-title", text: "colors (k)" }, grid);

    const gapArea = s("path", { class: "db-gap" }, svg);
    const upperLine = s("path", { class: "db-line upper" }, svg);
    const lowerLine = s("path", { class: "db-line lower" }, svg);
    const upperDot = s("circle", { r: 7, class: "db-dot upper" }, svg);
    const lowerDot = s("circle", { r: 7, class: "db-dot lower" }, svg);
    const upperLabel = s("text", { class: "db-label" }, svg);
    const lowerLabel = s("text", { class: "db-label" }, svg);
    const gapLabel = s("text", { class: "db-gap-label", "text-anchor": "middle", text: "gap" }, svg);

    const stepPath = (events: BoundEvent[], v0: number, t: number) => {
      let d = `M${X(0)},${Y(v0)}`;
      let v = v0;
      for (const e of events) {
        if (e.t > t) break;
        d += ` H${X(e.t)} V${Y(e.value)}`;
        v = e.value;
      }
      d += ` H${X(t)}`;
      return { d, v };
    };

    // ── verdicts + feed
    const side = h("div", { class: "db-side" });
    const counts = { verified: 0, refuted: 0, unknown: 0 };
    const bars = Object.fromEntries(
      (["verified", "refuted", "unknown"] as const).map((k) => {
        const fill = h("i");
        const num = h("b", { class: "mono", text: "0" });
        side.append(h("div", { class: `db-bar ${k}` }, h("span", { text: k }), h("div", { class: "db-bar-track" }, fill), num));
        return [k, { fill, num }];
      }),
    ) as Record<keyof typeof counts, { fill: HTMLElement; num: HTMLElement }>;
    const feed = h("div", { class: "db-feed mono" });
    side.append(h("div", { class: "db-feed-title", text: "event stream" }), feed);
    content.append(side, h("div", { class: "sim-note", text: "simulated data" }));

    let fed = 0;
    life.loop((sec) => {
      const p = Math.min(1, (sec * 1000) / RUN_MS);
      const t = p * SIM.minutes;
      const U = stepPath(sim.upper, SIM.upper0, t);
      const L = stepPath(sim.lower, SIM.lower0, t);
      upperLine.setAttribute("d", U.d);
      lowerLine.setAttribute("d", L.d);

      // shaded gap: upper path forward, lower path back
      const pts = (events: BoundEvent[], v0: number) => {
        const out: [number, number][] = [[0, v0]];
        let v = v0;
        for (const e of events) {
          if (e.t > t) break;
          out.push([e.t, v], [e.t, e.value]);
          v = e.value;
        }
        out.push([t, v]);
        return out;
      };
      const up = pts(sim.upper, SIM.upper0);
      const lo = pts(sim.lower, SIM.lower0).reverse();
      gapArea.setAttribute("d", "M" + [...up, ...lo].map(([a, b]) => `${X(a)},${Y(b)}`).join(" L") + " Z");

      upperDot.setAttribute("cx", String(X(t)));
      upperDot.setAttribute("cy", String(Y(U.v)));
      lowerDot.setAttribute("cx", String(X(t)));
      lowerDot.setAttribute("cy", String(Y(L.v)));
      upperLabel.setAttribute("x", String(X(t) + 22));
      upperLabel.setAttribute("y", String(Y(U.v) + 9));
      upperLabel.innerHTML = `<tspan class="v">${U.v}</tspan><tspan dx="10">best coloring</tspan>`;
      lowerLabel.setAttribute("x", String(X(t) + 22));
      lowerLabel.setAttribute("y", String(Y(L.v) + 9));
      lowerLabel.innerHTML = `<tspan class="v">${L.v}</tspan><tspan dx="10">proven lower bound</tspan>`;
      gapLabel.setAttribute("x", String(X(t / 2)));
      gapLabel.setAttribute("y", String((Y(sim.upperAt(t / 2)) + Y(sim.lowerAt(t / 2))) / 2 + 10));
      gapLabel.style.opacity = t > 6 ? "1" : "0";

      tUpper.textContent = String(U.v);
      tLower.textContent = String(L.v);
      tGap.textContent = String(U.v - L.v);
      const wobble = Math.sin(sec * 3.1) * 18 + Math.sin(sec * 7.7) * 9;
      tMsgs.textContent = p < 1 ? String(Math.round(210 + wobble)) : String(Math.round(60 + wobble / 3));
      tAgents.textContent = String(Math.min(48, 12 + Math.floor(sec * 9)));
      tQueue.textContent = p < 1 ? String(Math.max(0, Math.round(5 + Math.sin(sec * 1.7) * 5))) : "0";

      while (fed < sim.feed.length && sim.feed[fed]!.t <= t) {
        const e = sim.feed[fed++]!;
        counts[e.verdict]++;
        const row = h(
          "div",
          { class: `db-ev ${e.verdict}` },
          h("span", { class: "seq", text: `#${e.seq}` }),
          h("span", { class: "kind", text: e.kind }),
          h("span", { class: "txt", text: e.text }),
          h("b", { text: e.verdict }),
        );
        feed.prepend(row);
        while (feed.children.length > 9) feed.lastElementChild!.remove();
      }
      const max = Math.max(1, counts.verified + counts.refuted + counts.unknown);
      for (const k of ["verified", "refuted", "unknown"] as const) {
        bars[k].fill.style.width = `${(counts[k] / max) * 100}%`;
        bars[k].num.textContent = String(counts[k]);
      }
    });
  },
};
