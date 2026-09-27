import { h } from "../lib/dom";
import type { Scene } from "../scene";
import { frame, reveal } from "../scene";

type Row = { kind: string; dir: "upper" | "lower" | "edge" | "lead"; claim: string; cert: string; check: string; z3: boolean | "lead" };

const ROWS: Row[] = [
  { kind: "COLORING", dir: "upper", claim: "χ(G) ≤ k", cert: "a full coloring", check: "O(E) edge scan", z3: false },
  { kind: "CLIQUE", dir: "lower", claim: "χ(G) ≥ k", cert: "k vertices", check: "all pairs are edges", z3: false },
  { kind: "SUBGRAPH_BOUND", dir: "lower", claim: "χ(G) ≥ k", cert: "vertex set S", check: "G[S] not (k−1)-colorable", z3: true },
  { kind: "SEPARATION", dir: "edge", claim: "c(u) ≠ c(v) in every k-coloring", cert: "S containing u, v", check: "G[S] + c(u)=c(v) not k-colorable", z3: true },
  { kind: "MERGE", dir: "lead", claim: "c(u) = c(v) in some optimal coloring", cert: "none", check: "stays a lead", z3: "lead" },
];

const DIR_LABEL = { upper: "upper bound", lower: "lower bound", edge: "new edge", lead: "heuristic" };

export const claims: Scene = {
  id: "claim-types",
  kicker: "Claims carry certificates",
  title: "Checking never means solving",
  blurb:
    "Every claim comes with a small certificate. Colorings and cliques are checked in-process by scanning edges. Lower bounds and separations send only a small induced subgraph to Z3. Merges are heuristic and stay on the leads lane.",
  mount(root, life) {
    const content = frame(root, this);
    const table = h(
      "div",
      { class: "ct-table" },
      h(
        "div",
        { class: "ct-row ct-th" },
        h("span", { text: "kind" }),
        h("span", { text: "claim" }),
        h("span", { text: "certificate" }),
        h("span", { text: "check" }),
      ),
      ...ROWS.map((r) =>
        h(
          "div",
          { class: "ct-row rv" },
          h("span", { class: "ct-kind" }, h("b", { class: "mono", text: r.kind }), h("small", { class: `ct-dir ${r.dir}`, text: DIR_LABEL[r.dir] })),
          h("span", { class: "ct-claim", text: r.claim }),
          h("span", { class: "ct-cert", text: r.cert }),
          h(
            "span",
            { class: "ct-check" },
            h("span", { text: r.check }),
            h("span", {
              class: `tag ${r.z3 === true ? "z3" : r.z3 === "lead" ? "lead" : "fast"}`,
              text: r.z3 === true ? "Z3 · unsat" : r.z3 === "lead" ? "leads only" : "in-process",
            }),
          ),
        ),
      ),
    );
    content.append(table);
    table.querySelectorAll(".rv").forEach((el, i) => reveal(life, el, 250 + i * 450));
  },
};
