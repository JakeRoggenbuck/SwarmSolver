import { h } from "../lib/dom";
import type { Scene } from "../scene";
import { frame, reveal } from "../scene";

export const missing: Scene = {
  id: "missing",
  kicker: "The idea",
  title: "Two missing pieces",
  blurb:
    "Verification decides whether a finding is real. Communication makes sure every agent inherits it. Together they turn a swarm's lucky guesses into progress that compounds.",
  mount(root, life) {
    const content = frame(root, this);

    const card = (n: string, cls: string, verb: string, q: string, body: string, tag: string) =>
      h(
        "div",
        { class: `ms-card ${cls} rv` },
        h("div", { class: "ms-top" }, h("span", { class: "ms-n", text: n }), h("span", { class: "ms-verb", text: verb })),
        h("div", { class: "ms-q", text: q }),
        h("div", { class: "ms-body", html: body }),
        h("span", { class: "tag", text: tag }),
      );

    const a = card(
      "01",
      "verify",
      "Verify",
      "Is this finding real?",
      "A <b>Z3 harness</b> checks a small certificate before any agent builds on it.",
      "formal verification",
    );
    const b = card(
      "02",
      "share",
      "Share",
      "How does every agent inherit it?",
      "A <b>lock-free, ordered log</b> fans each finding out to every agent in real time.",
      "realtime messaging",
    );
    const plus = h("div", { class: "ms-plus rv", text: "+" });
    const bottom = h("div", {
      class: "caption rv",
      html: "Verified findings, shared instantly → <b>progress that compounds across the swarm.</b>",
    });
    content.append(h("div", { class: "ms-grid" }, a, plus, b), bottom);

    reveal(life, a, 200);
    reveal(life, plus, 700);
    reveal(life, b, 1000);
    reveal(life, bottom, 2000);
  },
};
