import { h, rng } from "../lib/dom";
import type { Scene } from "../scene";
import { frame } from "../scene";

const CLAIMS: [string, string][] = [
  ["SEPARATION", "c(12) ≠ c(88) · k=27"],
  ["SUBGRAPH_BOUND", "χ ≥ 24 · |S|=41"],
  ["COLORING", "χ ≤ 49 · full coloring"],
  ["CLIQUE", "χ ≥ 13 · 13 vertices"],
  ["SEPARATION", "c(3) ≠ c(41) · k=27"],
  ["SUBGRAPH_BOUND", "χ ≥ 25 · |S|=52"],
  ["SEPARATION", "c(7) ≠ c(219) · k=27"],
  ["COLORING", "χ ≤ 48 · full coloring"],
];

// Chip centers, in content-box pixels.
const LEAD_Y = 95;
const GATE = { x: 840, y: 345 };
const VER_Y = 595;

export const lanes: Scene = {
  id: "two-lanes",
  kicker: "Verification",
  title: "Two lanes: fast leads, proven results",
  blurb:
    "Every claim is visible to the swarm the moment it is posted on the leads lane. The harness then checks it. A proof promotes it to the verified lane; a refutation comes back with a counterexample; a timeout leaves it as an unknown lead. Agents only build on the verified lane.",
  mount(root, life) {
    const content = frame(root, this);
    const stage = h("div", { class: "ln-wrap" });
    content.append(stage);

    stage.append(
      h(
        "div",
        { class: "ln-lane lead" },
        h("div", { class: "ln-label" }, h("b", { text: "leads" }), h("small", { text: "unverified · instant" })),
      ),
      h(
        "div",
        { class: "ln-lane verified" },
        h("div", { class: "ln-label" }, h("b", { text: "verified" }), h("small", { text: "proven by the harness" })),
      ),
    );
    const gate = h(
      "div",
      { class: "ln-gate" },
      h("b", { text: "Z3 harness" }),
      h("small", { class: "mono", text: "checks the certificate" }),
    );
    const gateStatus = h("div", { class: "ln-gate-status mono", text: "idle" });
    gate.append(gateStatus);
    stage.append(gate);

    const counts = { verified: 0, refuted: 0, unknown: 0 };
    const countEls = Object.fromEntries(
      (["verified", "refuted", "unknown"] as const).map((k) => [k, h("b", { class: "mono", text: "0" })]),
    ) as Record<keyof typeof counts, HTMLElement>;
    stage.append(
      h(
        "div",
        { class: "ln-counts" },
        ...(["verified", "refuted", "unknown"] as const).map((k) =>
          h("div", { class: `ln-count ${k}` }, countEls[k], h("span", { text: k })),
        ),
      ),
    );

    const r = rng(5);
    let n = 0;
    let gateBusy = Promise.resolve();

    const run = async () => {
      const [kind, claim] = CLAIMS[n++ % CLAIMS.length]!;
      const chip = h("div", { class: "ln-chip" }, h("b", { text: kind }), h("span", { text: claim }));
      stage.append(chip);
      life.onDispose(() => chip.remove());
      const at = (x: number, y: number, scale = 1) => `translate(${x}px, ${y}px) translate(-50%, -50%) scale(${scale})`;

      // post to leads: instantly visible to every agent
      chip.style.transform = at(560, LEAD_Y);
      await chip.animate([{ opacity: 0, transform: at(560, LEAD_Y, 0.8) }, { opacity: 1, transform: at(560, LEAD_Y) }], {
        duration: 400,
        easing: "ease-out",
      }).finished;
      const ripple = h("div", { class: "ln-ripple" });
      ripple.style.transform = at(560, LEAD_Y);
      stage.append(ripple);
      ripple.animate([{ opacity: 0.8, transform: at(560, LEAD_Y, 0.9) }, { opacity: 0, transform: at(560, LEAD_Y, 1.6) }], {
        duration: 900,
        easing: "ease-out",
      }).finished.then(() => ripple.remove());

      await move(chip, at(560, LEAD_Y), at(GATE.x - 160, LEAD_Y), 900);

      // wait for the harness (one at a time, so the camera can follow each verdict)
      const prev = gateBusy;
      let release!: () => void;
      gateBusy = new Promise((res) => (release = res));
      await prev;
      if (!life.alive) return;

      await move(chip, at(GATE.x - 160, LEAD_Y), at(GATE.x, GATE.y, 0.92), 650);
      gate.classList.add("busy");
      gateStatus.textContent = "checking…";
      delete gateStatus.dataset.state;
      await life.wait(900);
      const roll = r();
      const outcome = roll < 0.62 ? "verified" : roll < 0.86 ? "refuted" : "unknown";
      gate.classList.remove("busy");
      gateStatus.textContent =
        outcome === "verified" ? "unsat → proven" : outcome === "refuted" ? "sat → counterexample" : "timeout (-T:10)";
      gateStatus.dataset.state = outcome;
      chip.classList.add(outcome);
      counts[outcome]++;
      countEls[outcome].textContent = String(counts[outcome]);
      release();

      if (outcome === "verified") {
        await move(chip, at(GATE.x, GATE.y, 0.92), at(GATE.x + 160, VER_Y), 700);
        await move(chip, at(GATE.x + 160, VER_Y), at(1400, VER_Y), 1100);
        await chip.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 500, delay: 300, fill: "forwards" }).finished;
      } else if (outcome === "refuted") {
        await chip.animate(
          [0, -14, 14, -10, 10, 0].map((dx) => ({ transform: `translate(${GATE.x + dx}px, ${GATE.y}px) translate(-50%, -50%) scale(0.92)` })),
          { duration: 500 },
        ).finished;
        await move(chip, at(GATE.x, GATE.y, 0.92), at(GATE.x + 420, LEAD_Y), 800);
        await chip.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 500, delay: 400, fill: "forwards" }).finished;
      } else {
        await move(chip, at(GATE.x, GATE.y, 0.92), at(GATE.x + 420, LEAD_Y), 800);
        await chip.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 500, delay: 400, fill: "forwards" }).finished;
      }
      chip.remove();
    };

    const move = (el: HTMLElement, from: string, to: string, ms: number) => {
      el.style.transform = to;
      return el.animate([{ transform: from }, { transform: to }], { duration: ms, easing: "cubic-bezier(.5,0,.2,1)" })
        .finished;
    };

    void run();
    life.every(1900, () => void run());
  },
};
