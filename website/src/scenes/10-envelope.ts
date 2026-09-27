import { h, s } from "../lib/dom";
import { svgRoot } from "../lib/svgkit";
import type { Scene } from "../scene";
import { frame, reveal } from "../scene";

const K = (k: string) => `<span class="j-k">"${k}"</span>`;
const S_ = (v: string) => `<span class="j-s">"${v}"</span>`;
const N = (v: string | number) => `<span class="j-n">${v}</span>`;

const LINES: { html: string; note?: string; mark?: string }[] = [
  { html: "{" },
  { html: `  ${K("seq")}: ${N(1042)},`, note: "global order", mark: "wire" },
  { html: `  ${K("topic")}: ${S_("leads")},`, note: "lane", mark: "lead" },
  { html: `  ${K("agent")}: ${S_("a17")},` },
  { html: `  ${K("ts")}: ${N(1727460000123)},` },
  { html: `  ${K("kind")}: ${S_("SEPARATION")},` },
  { html: `  ${K("id")}: ${S_("sha256:9f2c…")},`, note: "dedup key" },
  { html: `  ${K("parents")}: [${S_("sha256:41ab…")}],`, note: "provenance", mark: "z3" },
  { html: `  ${K("claim")}: { ${K("u")}: ${N(12)}, ${K("v")}: ${N(88)}, ${K("k")}: ${N(27)},` },
  { html: `             ${K("S")}: [${N(12)}, ${N(88)}, ${N(3)}, ${N(41)}] },`, note: "certificate", mark: "verified" },
  { html: `  ${K("note")}: ${S_("≤200 chars rationale")}` },
  { html: "}" },
];

type DagNode = { id: string; x: number; y: number; kind: string; detail: string; hash: string; parents: string[]; state: "verified" | "lead" | "root" };

// Illustrative DAG. The node for the envelope on the left is "sep1288".
const DAG: DagNode[] = [
  { id: "g", x: 400, y: 40, kind: "PROBLEM", detail: "DIMACS graph G", hash: "", parents: [], state: "root" },
  { id: "clq", x: 130, y: 190, kind: "CLIQUE", detail: "χ ≥ 13", hash: "c07e…", parents: ["g"], state: "verified" },
  { id: "sep341", x: 400, y: 190, kind: "SEPARATION", detail: "c(3) ≠ c(41)", hash: "41ab…", parents: ["g"], state: "verified" },
  { id: "sep719", x: 670, y: 190, kind: "SEPARATION", detail: "c(7) ≠ c(19)", hash: "d2e8…", parents: ["g"], state: "verified" },
  { id: "sep1288", x: 265, y: 350, kind: "SEPARATION", detail: "c(12) ≠ c(88)", hash: "9f2c…", parents: ["sep341"], state: "lead" },
  { id: "sub24", x: 560, y: 350, kind: "SUBGRAPH_BOUND", detail: "χ ≥ 24", hash: "77b1…", parents: ["clq", "sep341", "sep719"], state: "verified" },
  { id: "sub25", x: 400, y: 510, kind: "SUBGRAPH_BOUND", detail: "χ ≥ 25", hash: "e41d…", parents: ["sep1288", "sub24"], state: "verified" },
];

const NW = 236;
const NH = 84;

export const envelope: Scene = {
  id: "envelope-provenance",
  kicker: "The envelope",
  title: "Every message records what it built on",
  blurb:
    "Each message is a small JSON envelope: a global sequence number, a lane, the claim, and its certificate. The parents field lists the verified findings a claim built on, which gives a provenance DAG: which insight unlocked which.",
  loop: 16000,
  mount(root, life) {
    const content = frame(root, this);

    const code = h("div", { class: "ev-code" });
    const lines = LINES.map((l) =>
      h(
        "div",
        { class: `ev-line${l.mark ? ` mark-${l.mark}` : ""}` },
        h("code", { html: l.html }),
        l.note ? h("span", { class: "ev-note", text: l.note }) : null,
      ),
    );
    code.append(h("div", { class: "ev-bar mono" }, h("i"), h("i"), h("i"), h("span", { text: "WS /ws?from=1041&topics=leads,verified" })), ...lines);
    content.append(code);

    const svg = svgRoot(820, 600);
    const wrap = h("div", { class: "ev-dag" }, h("div", { class: "ev-dag-title mono", text: "provenance DAG · illustrative" }), svg);
    content.append(wrap);

    const edgesG = s("g", {}, svg);
    const nodesG = s("g", {}, svg);
    const byId = new Map(DAG.map((n) => [n.id, n]));
    const nodeEls = new Map<string, SVGGElement>();
    const edgeEls: { el: SVGPathElement; to: string }[] = [];

    for (const n of DAG) {
      for (const p of n.parents) {
        const a = byId.get(p)!;
        const y1 = a.y + NH / 2;
        const y2 = n.y - NH / 2;
        const el = s(
          "path",
          { d: `M${a.x},${y1} C${a.x},${(y1 + y2) / 2} ${n.x},${(y1 + y2) / 2} ${n.x},${y2 - 6}`, class: "ev-edge", pathLength: 100, "marker-end": "url(#arrow)" },
          edgesG,
        );
        edgeEls.push({ el, to: n.id });
      }
      const g = s("g", { class: `ev-node ${n.state}`, transform: `translate(${n.x - NW / 2}, ${n.y - NH / 2})` }, nodesG);
      s("rect", { width: NW, height: NH, rx: 14 }, g);
      s("text", { x: 18, y: 32, class: "ev-kind", text: n.kind }, g);
      s("text", { x: 18, y: 64, class: "ev-detail", text: n.detail }, g);
      if (n.hash) s("text", { x: NW - 16, y: 64, "text-anchor": "end", class: "ev-hash", text: n.hash }, g);
      nodeEls.set(n.id, g);
    }

    lines.forEach((l, i) => reveal(life, l, 200 + i * 140));
    const t0 = 200 + LINES.length * 140 + 300;
    const show = (id: string) => {
      nodeEls.get(id)!.classList.add("in");
      edgeEls.filter((e) => e.to === id).forEach((e) => e.el.classList.add("in"));
    };
    const order = ["g", "clq", "sep341", "sep719", "sub24", "sep1288"];
    order.forEach((id, i) => life.after(t0 + i * 600, () => show(id)));
    const tLead = t0 + (order.length - 1) * 600;
    // the envelope on the left arrives as a lead that builds on 41ab…
    life.after(tLead, () => {
      lines[7]!.classList.add("hot");
      nodeEls.get("sep1288")!.classList.add("hot");
    });
    // the harness proves it, and only then does anyone build on it
    life.after(tLead + 1600, () => {
      const n = nodeEls.get("sep1288")!;
      n.classList.remove("lead", "hot");
      n.classList.add("verified", "promoted");
      lines[7]!.classList.remove("hot");
    });
    life.after(tLead + 2600, () => show("sub25"));
  },
};
