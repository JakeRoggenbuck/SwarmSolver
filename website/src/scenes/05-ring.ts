import { h, s } from "../lib/dom";
import { svgRoot } from "../lib/svgkit";
import type { Scene } from "../scene";
import { frame, reveal } from "../scene";

const SLOTS = 24;
const CX = 390;
const CY = 345;
const R_OUT = 285;
const R_IN = 220;

const STATUS_COLOR: Record<string, string> = {
  "caught up": "#3ddc97",
  batching: "#5aa9ff",
  lagging: "#ffb547",
  snapshot: "#5aa9ff",
};

function polar(r: number, deg: number) {
  const a = (deg * Math.PI) / 180;
  return [CX + r * Math.cos(a), CY + r * Math.sin(a)] as const;
}

function slotAngle(i: number) {
  return -90 + (i % SLOTS) * (360 / SLOTS) + 360 / SLOTS / 2;
}

function sector(i: number) {
  const step = 360 / SLOTS;
  const a0 = -90 + i * step + 1.2;
  const a1 = a0 + step - 2.4;
  const [x0, y0] = polar(R_OUT, a0);
  const [x1, y1] = polar(R_OUT, a1);
  const [x2, y2] = polar(R_IN, a1);
  const [x3, y3] = polar(R_IN, a0);
  return `M${x0},${y0} A${R_OUT},${R_OUT} 0 0 1 ${x1},${y1} L${x2},${y2} A${R_IN},${R_IN} 0 0 0 ${x3},${y3} Z`;
}

export const ring: Scene = {
  id: "ring-buffer",
  kicker: "Fan-out without locks",
  title: "A ring buffer with one writer",
  blurb:
    "The sequencer is the only writer. It publishes a new head with an atomic store, then closes a generation channel so one close() wakes every reader. Each reader sends everything from its cursor to head in one batched write. A client that falls a full ring behind gets a snapshot and jumps to head, so it never blocks anyone.",
  mount(root, life) {
    const content = frame(root, this);
    const svg = svgRoot(800, 690);
    svg.classList.add("rb-svg");
    content.append(svg);

    const slots = Array.from({ length: SLOTS }, (_, i) =>
      s("path", { d: sector(i), fill: "#1b2230", stroke: "#07090d", "stroke-width": 2 }, svg),
    );
    const written = new Array<number>(SLOTS).fill(-1);

    const pulse = s("circle", { cx: CX, cy: CY, r: R_IN - 4, fill: "none", stroke: "#5aa9ff", "stroke-width": 3, opacity: 0, class: "rb-pulse" }, svg);
    s("text", { x: CX, y: CY - 44, "text-anchor": "middle", class: "rb-center-label", text: "head seq" }, svg);
    const headText = s("text", { x: CX, y: CY + 26, "text-anchor": "middle", class: "rb-center-num", text: "0" }, svg);
    const genText = s("text", { x: CX, y: CY + 70, "text-anchor": "middle", class: "rb-gen", text: "close(gen) → wake all" }, svg);

    const headMark = s("g", {}, svg);
    s("path", { d: "M0,-14 L12,8 L-12,8 Z", fill: "#eef2f8" }, headMark);
    const headLabel = s("text", { class: "rb-head", "text-anchor": "middle", text: "head" }, svg);

    type Reader = {
      name: string;
      desc: string;
      radius: number;
      cursor: number;
      status: string;
      label: string;
      g: SVGGElement;
      ring: SVGCircleElement;
      row?: { cursor: HTMLElement; lag: HTMLElement; status: HTMLElement; dot: HTMLElement };
    };
    const mk = (name: string, desc: string, radius: number): Reader => {
      const g = s("g", {}, svg);
      const ringEl = s("circle", { r: 20, fill: "#0e1219", stroke: "#3ddc97", "stroke-width": 3 }, g);
      s("text", { y: 7, "text-anchor": "middle", class: "rb-reader", text: name }, g);
      return { name, desc, radius, cursor: 0, status: "caught up", label: "caught up", g, ring: ringEl };
    };
    const readers = [mk("R1", "fast client", 190), mk("R2", "batched writes", 150), mk("R3", "slow client", 110)];

    // right-hand panel
    const table = h("div", { class: "rb-table rv" });
    table.append(
      h("div", { class: "rb-row rb-th" }, h("span", { text: "reader" }), h("span", { text: "cursor" }), h("span", { text: "lag" }), h("span", { text: "status" })),
    );
    for (const rd of readers) {
      const dot = h("i");
      const cursor = h("span", { class: "mono" });
      const lag = h("span", { class: "mono" });
      const status = h("span", { class: "rb-status" });
      table.append(
        h("div", { class: "rb-row" }, h("span", { class: "rb-name" }, dot, h("b", { text: rd.name }), h("small", { text: rd.desc })), cursor, lag, status),
      );
      rd.row = { cursor, lag, status, dot };
    }
    const points = h(
      "ul",
      { class: "rb-points" },
      ...[
        "<b>One writer.</b> The fan-out path takes no locks.",
        "<b>Encode once</b>, send the same bytes to N clients.",
        "<b>One close()</b> wakes every waiting reader.",
        "<b>Slow clients</b> get a snapshot. They never block the sequencer.",
      ].map((html) => h("li", { class: "rv", html })),
    );
    const panel = h("div", { class: "rb-panel" }, table, points);
    content.append(panel);
    reveal(life, table, 200);
    points.querySelectorAll("li").forEach((li, i) => reveal(life, li, 600 + i * 300));

    // simulation
    let head = 0;
    const place = (rd: Reader) => {
      const [x, y] = polar(rd.radius, slotAngle(rd.cursor));
      rd.g.style.transform = `translate(${x}px, ${y}px)`;
      const color = STATUS_COLOR[rd.status]!;
      rd.ring.setAttribute("stroke", color);
      rd.row!.dot.style.background = color;
      rd.row!.cursor.textContent = String(rd.cursor);
      rd.row!.lag.textContent = String(head - rd.cursor);
      rd.row!.status.textContent = rd.label;
      rd.row!.status.style.color = color;
    };
    readers.forEach((rd) => place(rd));

    const paint = () => {
      const now = performance.now();
      const slow = readers[2]!;
      for (let i = 0; i < SLOTS; i++) {
        const w = written[i]!;
        const seqAt = w;
        const inBacklog = seqAt >= slow.cursor && seqAt < head;
        const age = w < 0 ? 99 : (now - (slotTime[i] ?? 0)) / 1000;
        const fresh = Math.max(0, 1 - age / 2.5);
        slots[i]!.setAttribute("fill", w < 0 ? "#1b2230" : mix(fresh));
        slots[i]!.setAttribute("stroke", inBacklog ? "#ffb547" : "#07090d");
        slots[i]!.setAttribute("stroke-width", inBacklog ? "3" : "2");
      }
      const [hx, hy] = polar(R_OUT + 26, slotAngle(Math.max(0, head - 1)));
      const ang = slotAngle(Math.max(0, head - 1)) + 90 + 180;
      headMark.setAttribute("transform", `translate(${hx},${hy}) rotate(${ang})`);
      const [lx, ly] = polar(R_OUT + 62, slotAngle(Math.max(0, head - 1)));
      headLabel.setAttribute("x", String(lx));
      headLabel.setAttribute("y", String(ly + 9));
    };
    const slotTime: number[] = [];
    life.loop(paint);

    const tick = () => {
      const i = head % SLOTS;
      written[i] = head;
      slotTime[i] = performance.now();
      head++;
      headText.textContent = String(head);
      // generation channel closed → every reader wakes
      pulse.animate(
        [
          { transform: "scale(0.25)", opacity: 0.9 },
          { transform: "scale(1)", opacity: 0 },
        ],
        { duration: 600, easing: "ease-out" },
      );
      genText.animate([{ opacity: 1 }, { opacity: 0.25 }], { duration: 500, fill: "forwards" });
      const r1 = readers[0]!;
      life.after(90, () => {
        r1.cursor = head;
        r1.status = r1.label = "caught up";
        place(r1);
      });
      readers.forEach((rd) => place(rd));
    };
    life.every(300, tick);

    life.every(950, () => {
      const r2 = readers[1]!;
      const n = head - r2.cursor;
      r2.cursor = head;
      r2.status = "batching";
      r2.label = `batch ×${n}`;
      place(r2);
    });

    life.every(1400, () => {
      const r3 = readers[2]!;
      if (r3.status === "snapshot") r3.status = "lagging";
      if (head - r3.cursor >= SLOTS - 1) {
        r3.status = "snapshot";
        r3.cursor = head;
        r3.ring.animate([{ transform: "scale(1.8)" }, { transform: "scale(1)" }], { duration: 600, easing: "ease-out" });
        r3.label = "snapshot → jump";
        place(r3);
        return;
      }
      r3.cursor = Math.min(head, r3.cursor + 1);
      r3.status = r3.label = head - r3.cursor > 3 ? "lagging" : "caught up";
      place(r3);
    });
  },
};

function mix(t: number) {
  // blend from the panel color (#1b2230) to the wire color (#5aa9ff)
  const a = [0x1b, 0x22, 0x30];
  const b = [0x5a, 0xa9, 0xff];
  const base = 0.35;
  const k = base + (1 - base) * t;
  return `rgb(${a.map((v, i) => Math.round(v + (b[i]! - v) * k)).join(",")})`;
}
