import { s, rng } from "../lib/dom";
import { box, flash, svgRoot, travel } from "../lib/svgkit";
import type { Scene } from "../scene";
import { frame } from "../scene";

const LEAD = "#ffb547";
const VERIFIED = "#3ddc97";
const REFUTED = "#ff5c6c";
const WIRE = "#5aa9ff";
const Z3 = "#b18cff";

export const architecture: Scene = {
  id: "architecture",
  kicker: "Server architecture",
  title: "One writer, lock-free fan-out",
  blurb:
    "Agents publish over one WebSocket each. A single sequencer stamps every message with a global sequence number, encodes it once, and writes it to a ring buffer. One reader goroutine per client streams from the ring. Leads also go to the Z3 verifier pool, whose verdicts re-enter the log as events.",
  mount(root, life) {
    const content = frame(root, this);
    const svg = svgRoot(1680, 690);
    content.append(svg);
    const wires = s("g", {}, svg);
    const boxes = s("g", {}, svg);
    const packets = s("g", {}, svg);

    const MID = 312;
    const ys = [0, 1, 2, 3, 4, 5].map((i) => 87 + i * 90);

    const wire = (d: string, dashed = false) =>
      s(
        "path",
        {
          d,
          fill: "none",
          stroke: "#2f3a4e",
          "stroke-width": 2.5,
          "stroke-dasharray": dashed ? "6 8" : undefined,
          "marker-end": "url(#arrow)",
        },
        wires,
      );

    // columns
    s("text", { x: 95, y: 30, "text-anchor": "middle", class: "col-label", text: "agents" }, boxes);
    s("text", { x: 1585, y: 30, "text-anchor": "middle", class: "col-label", text: "subscribers" }, boxes);

    const inPills = ys.map((y, i) => pill(boxes, 0, y - 27, 190, `a${String(i * 7 + 3).padStart(2, "0")}`));
    const outPills = ys.map((y, i) =>
      i === 5 ? pill(boxes, 1490, y - 27, 190, "dashboard", WIRE) : pill(boxes, 1490, y - 27, 190, `a${String(i * 7 + 3).padStart(2, "0")}`),
    );

    const inPaths = ys.map((y) => wire(`M190,${y} C245,${y} 235,${MID} 288,${MID}`));
    const outPaths = ys.map((y) => wire(`M1440,${MID} C1470,${MID} 1458,${y} 1488,${y}`));
    const pIngestSeq = wire(`M480,${MID} L558,${MID}`);
    const pSeqRing = wire(`M800,${MID} L878,${MID}`);
    const pRingRead = wire(`M1200,${MID} L1268,${MID}`);
    const pSeqVer = wire(`M700,392 L700,518`);
    const pVerIngest = wire(`M440,575 L385,575 L385,369`);
    const pSeqState = wire(`M680,232 L680,95 L878,95`);
    const pStateRead = wire(`M1200,95 L1355,95 L1355,240`, true);

    // wire labels
    const wl = (x: number, y: number, t: string, anchor = "start", color = "#8b95a8") =>
      s("text", { x, y, "text-anchor": anchor, class: "wire-label", fill: color, text: t }, boxes);
    wl(95, 610, "1 WebSocket each", "middle");
    wl(712, 462, "leads → verify", "start", LEAD);
    wl(372, 470, "verdicts", "end", VERIFIED);
    wl(1368, 172, "snapshot", "start", WIRE);
    wl(760, 84, "fold verified", "middle");

    const ingest = box(boxes, 290, 257, 190, 110, "ingest", "chan Envelope");
    const seq = box(boxes, 560, 232, 240, 160, "sequencer", "seq #1041");
    const seqSub = seq.g.querySelector(".bx-sub")!;
    const ver = box(boxes, 440, 520, 300, 110, "verifier pool", "z3 -in -T:10", Z3);
    const state = box(boxes, 880, 40, 320, 110, "state view", "atomic.Pointer snapshot");
    const readers = box(boxes, 1270, 242, 170, 140, "readers", "1 per client");

    // ring buffer
    const ringG = s("g", {}, boxes);
    const ringRect = s(
      "rect",
      { x: 880, y: 242, width: 320, height: 140, rx: 16, fill: "#0e1219", stroke: "#2f3a4e", "stroke-width": 2 },
      ringG,
    );
    s("text", { x: 1040, y: 280, "text-anchor": "middle", class: "bx-label", text: "ring buffer" }, ringG);
    const CELLS = 12;
    const cells = Array.from({ length: CELLS }, (_, i) =>
      s("rect", { x: 902 + i * 23.5, y: 296, width: 19, height: 34, rx: 4, fill: "#1b2230", class: "cell" }, ringG),
    );
    const head = s("path", { d: "M0,0 l-7,-11 h14 z", fill: "#eef2f8" }, ringG);
    s("text", { x: 1040, y: 364, "text-anchor": "middle", class: "bx-sub", text: "2¹⁶ slots · atomic head" }, ringG);

    // legend
    const legend = s("g", { transform: "translate(1040, 470)" }, boxes);
    [
      [LEAD, "lead (unverified)"],
      [VERIFIED, "verified verdict"],
      [REFUTED, "refuted verdict"],
      [WIRE, "snapshot"],
    ].forEach(([c, t], i) => {
      s("circle", { cx: 0, cy: i * 40, r: 8, fill: c }, legend);
      s("text", { x: 22, y: i * 40 + 8, class: "legend-text", text: t }, legend);
    });

    let seqNo = 1041;
    let slot = 0;
    const stamp = (color: string) => {
      seqNo++;
      seqSub.textContent = `seq #${seqNo}`;
      flash(seq.rect, color, life, 350);
      const c = cells[slot % CELLS]!;
      c.setAttribute("fill", color);
      c.style.opacity = "1";
      life.after(1600, () => c.setAttribute("fill", "#1b2230"));
      head.setAttribute("transform", `translate(${902 + (slot % CELLS) * 23.5 + 9.5}, 294)`);
      slot++;
    };
    head.setAttribute("transform", `translate(911.5, 294)`);

    const r = rng(11);

    const broadcast = async (color: string) => {
      await travel(life, packets, pIngestSeq, color, 260);
      stamp(color);
      await travel(life, packets, pSeqRing, color, 240);
      await travel(life, packets, pRingRead, color, 220);
      flash(readers.rect, color, life, 350);
      await Promise.all(
        outPaths.map((p, j) =>
          travel(life, packets, p, color, 480, 7).then(() => flash(outPills[j]!, color, life, 400)),
        ),
      );
    };

    const event = async () => {
      const i = Math.floor(r() * ys.length);
      flash(inPills[i]!, LEAD, life, 600);
      await travel(life, packets, inPaths[i]!, LEAD, 650);
      flash(ingest.rect, LEAD, life, 300);
      const fan = broadcast(LEAD);
      await life.wait(260);
      if (r() < 0.75) {
        await travel(life, packets, pSeqVer, LEAD, 420);
        flash(ver.rect, Z3, life, 800);
        ver.rect.classList.add("thinking");
        await life.wait(800);
        ver.rect.classList.remove("thinking");
        const ok = r() < 0.72;
        const color = ok ? VERIFIED : REFUTED;
        await travel(life, packets, pVerIngest, color, 520);
        flash(ingest.rect, color, life, 300);
        await broadcast(color);
        if (ok) {
          await travel(life, packets, pSeqState, VERIFIED, 500);
          flash(state.rect, VERIFIED, life, 600);
          if (r() < 0.5) {
            await travel(life, packets, pStateRead, WIRE, 600);
            flash(readers.rect, WIRE, life, 400);
          }
        }
      }
      await fan;
    };

    event();
    life.every(1300, () => void event());
  },
};

function pill(parent: Element, x: number, y: number, w: number, label: string, color = "#2f3a4e") {
  const g = s("g", {}, parent);
  const rect = s("rect", { x, y, width: w, height: 54, rx: 27, fill: "#0e1219", stroke: color, "stroke-width": 2 }, g);
  s("circle", { cx: x + 30, cy: y + 27, r: 7, fill: color === "#2f3a4e" ? "#8b95a8" : color }, g);
  s("text", { x: x + 50, y: y + 35, class: "pill-label", text: label }, g);
  return rect;
}
