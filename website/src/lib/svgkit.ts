import { s, ease } from "./dom";
import type { Life } from "./life";

/** Glow filter + arrow marker defs, shared by the SVG diagrams. */
export function defs(svg: SVGSVGElement) {
  const d = s("defs", {}, svg);
  const f = s("filter", { id: "glow", x: "-100%", y: "-100%", width: "300%", height: "300%" }, d);
  s("feGaussianBlur", { stdDeviation: 5, result: "b" }, f);
  const m = s("feMerge", {}, f);
  s("feMergeNode", { in: "b" }, m);
  s("feMergeNode", { in: "SourceGraphic" }, m);
  const mk = s(
    "marker",
    { id: "arrow", viewBox: "0 0 10 10", refX: 8, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" },
    d,
  );
  s("path", { d: "M0,0 L10,5 L0,10 z", fill: "#4a5468" }, mk);
  return d;
}

export function svgRoot(w: number, h: number) {
  const svg = s("svg", { viewBox: `0 0 ${w} ${h}`, width: w, height: h });
  defs(svg);
  return svg;
}

/** A labeled box. Returns the group and the rect so callers can flash it. */
export function box(
  parent: Element,
  x: number,
  y: number,
  w: number,
  hgt: number,
  label: string,
  sub?: string,
  color = "#2f3a4e",
) {
  const g = s("g", { class: "bx" }, parent);
  const rect = s(
    "rect",
    { x, y, width: w, height: hgt, rx: 16, fill: "#0e1219", stroke: color, "stroke-width": 2 },
    g,
  );
  const cy = sub ? y + hgt / 2 - 10 : y + hgt / 2 + 10;
  s("text", { x: x + w / 2, y: cy, "text-anchor": "middle", class: "bx-label", text: label }, g);
  if (sub) s("text", { x: x + w / 2, y: y + hgt / 2 + 26, "text-anchor": "middle", class: "bx-sub", text: sub }, g);
  return { g, rect };
}

/** Moves a glowing dot along a path. Resolves when it arrives. */
export async function travel(
  life: Life,
  layer: Element,
  path: SVGPathElement,
  color: string,
  ms: number,
  r = 9,
) {
  const len = path.getTotalLength();
  const dot = s("circle", { r, fill: color, filter: "url(#glow)" }, layer);
  life.onDispose(() => dot.remove());
  await life.tween(ms, (t) => {
    const p = path.getPointAtLength(len * ease.inOut(t));
    dot.setAttribute("cx", String(p.x));
    dot.setAttribute("cy", String(p.y));
  });
  dot.remove();
}

/** Briefly highlight an element's stroke (e.g. a box when a packet lands). */
export function flash(el: SVGElement, color: string, life: Life, ms = 500) {
  // remember the resting stroke once, so overlapping flashes never get stuck
  if (el.dataset.stroke === undefined) el.dataset.stroke = el.getAttribute("stroke") ?? "";
  const prev = el.dataset.stroke;
  el.setAttribute("stroke", color);
  el.style.filter = `drop-shadow(0 0 12px ${color})`;
  const token = Symbol();
  (el as any).__flash = token;
  life.after(ms, () => {
    if ((el as any).__flash !== token) return;
    el.setAttribute("stroke", prev);
    el.style.filter = "";
  });
}
