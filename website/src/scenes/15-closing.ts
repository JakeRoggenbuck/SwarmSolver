import { h } from "../lib/dom";
import type { Scene } from "../scene";
import { reveal } from "../scene";

export const closing: Scene = {
  id: "closing",
  kicker: "Summary",
  title: "Swarm",
  blurb: "Closing card: the three ideas in one frame.",
  mount(root, life) {
    const pillars = [
      ["01", "wire", "Realtime ordered log", "One sequencer, a lock-free ring buffer, and one reader per client. Replay from any offset."],
      ["02", "z3", "Z3-checked certificates", "Small induced subgraphs, so every check is fast, even on huge graphs."],
      ["03", "verified", "Findings that compound", "Verified separations become edges that every agent inherits."],
    ] as const;

    const wrap = h(
      "div",
      { class: "cl-wrap" },
      h("div", { class: "cl-word rv" }, h("i"), "Swarm"),
      h("div", { class: "cl-sub rv", text: "Verified realtime communication for agent swarms" }),
      h(
        "div",
        { class: "cl-pillars" },
        ...pillars.map(([n, c, t, b]) =>
          h("div", { class: `cl-pillar ${c} rv` }, h("span", { class: "mono", text: n }), h("b", { text: t }), h("p", { text: b })),
        ),
      ),
      h("div", { class: "cl-stack mono rv", text: "Go  ·  WebSocket  ·  Z3  ·  Claude" }),
    );
    root.append(wrap);
    wrap.querySelectorAll(".rv").forEach((el, i) => reveal(life, el, 200 + i * 350));
  },
};
