import { h, rng } from "../lib/dom";
import type { Scene } from "../scene";
import { frame, reveal } from "../scene";

export const divide: Scene = {
  id: "divide",
  kicker: "The problem",
  title: "Labs run swarms. Individuals run one agent.",
  blurb:
    "The gap isn't only compute. Labs have two things individuals don't: formal proofs that tell a real result from a compelling lead, and a way for a swarm to share what it finds.",
  mount(root, life) {
    const content = frame(root, this);
    const r = rng(3);

    const swarm = h("div", { class: "dv-swarm" });
    for (let i = 0; i < 26 * 9; i++) {
      const dot = h("i");
      dot.style.animationDelay = `${(r() * 3).toFixed(2)}s`;
      dot.style.animationDuration = `${(1.6 + r() * 2.2).toFixed(2)}s`;
      if (r() < 0.18) dot.classList.add(r() < 0.6 ? "g" : "a");
      swarm.append(dot);
    }

    const solo = h("div", { class: "dv-solo" }, h("i"));

    const col = (cls: string, label: string, sub: string, viz: HTMLElement, items: [boolean, string][]) =>
      h(
        "div",
        { class: `dv-col ${cls} rv` },
        h("div", { class: "dv-head" }, h("span", { text: label }), h("small", { text: sub })),
        viz,
        h(
          "ul",
          {},
          ...items.map(([ok, text]) =>
            h("li", { class: "rv" }, h("b", { class: ok ? "ok" : "no", text: ok ? "✓" : "✕" }), text),
          ),
        ),
      );

    const left = col("labs", "AI labs", "thousands of agents", swarm, [
      [true, "Thousands of agents in parallel"],
      [true, "Formal proofs check every result"],
      [true, "Findings shared across the swarm"],
    ]);
    const right = col("solo", "Individuals", "one agent, one chat", solo, [
      [false, "One agent at a time"],
      [false, "Can't tell a real result from a lead"],
      [false, "Findings passed around as markdown"],
    ]);
    content.append(h("div", { class: "dv-grid" }, left, right));

    reveal(life, left, 200);
    reveal(life, right, 700);
    left.querySelectorAll("li").forEach((el, i) => reveal(life, el, 1400 + i * 350));
    right.querySelectorAll("li").forEach((el, i) => reveal(life, el, 1550 + i * 350));
  },
};
