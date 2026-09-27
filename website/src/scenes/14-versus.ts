import { h } from "../lib/dom";
import { SIM, simulate } from "../lib/sim";
import type { Scene } from "../scene";
import { frame, reveal } from "../scene";

const RUN_MS = 11000;
const K_MIN = 10;
const K_MAX = 62;

export const versus: Scene = {
  id: "z3-vs-swarm",
  kicker: "The demo script",
  title: "Z3 alone times out. The swarm doesn't.",
  blurb:
    "First, run Z3 by itself on the full graph: it times out and the bounds don't move. Then run the swarm: agents keep posting small verified findings, and both bounds move toward each other. The numbers in this scene are illustrative.",
  loop: RUN_MS + 6000,
  mount(root, life) {
    const content = frame(root, this);
    const sim = simulate();

    const panel = (cls: string, title: string, sub: string) => {
      const clock = h("b", { class: "vs-clock mono", text: "00:00" });
      const status = h("div", { class: "vs-status mono" });
      const bar = h("div", { class: "vs-bar" });
      const lo = h("span", { class: "vs-lo mono" });
      const hi = h("span", { class: "vs-hi mono" });
      const range = h("div", { class: "vs-range" }, lo, hi);
      bar.append(range);
      const scale = h("div", { class: "vs-scale mono" }, ...[10, 20, 30, 40, 50, 60].map((k) => h("span", { text: String(k), style: `left:${pct(k)}%` })));
      const count = h("div", { class: "vs-count" });
      const el = h(
        "div",
        { class: `vs-panel ${cls} rv` },
        h("div", { class: "vs-head" }, h("div", {}, h("h3", { text: title }), h("small", { text: sub })), clock),
        h("div", { class: "vs-bar-label", text: "χ(G) is somewhere in here" }),
        bar,
        scale,
        status,
        count,
      );
      return { el, clock, status, range, lo, hi, count };
    };

    const solo = panel("solo", "Z3 on the full graph", "one solver · k-colorability of G");
    const swarm = panel("swarm", "The swarm", "48 agents · verified findings");
    content.append(h("div", { class: "vs-grid" }, solo.el, swarm.el), h("div", { class: "sim-note", text: "illustrative" }));
    reveal(life, solo.el, 150);
    reveal(life, swarm.el, 450);

    const setRange = (p: ReturnType<typeof panel>, lo: number, hi: number) => {
      p.range.style.left = `${pct(lo)}%`;
      p.range.style.width = `${pct(hi) - pct(lo)}%`;
      p.lo.textContent = String(lo);
      p.hi.textContent = String(hi);
    };
    setRange(solo, SIM.lower0, SIM.upper0);
    setRange(swarm, SIM.lower0, SIM.upper0);
    solo.status.textContent = "solving…";
    swarm.status.textContent = "agents connecting…";

    life.loop((sec) => {
      const p = Math.min(1, (sec * 1000) / RUN_MS);
      // both clocks show simulated wall time: up to a 60 minute run
      const mins = p * SIM.minutes;
      const txt = `${String(Math.floor(mins)).padStart(2, "0")}:${String(Math.floor((mins % 1) * 60)).padStart(2, "0")}`;
      swarm.clock.textContent = txt;
      solo.clock.textContent = p < 0.18 ? txt : "10:00";
      if (p >= 0.18 && !solo.el.classList.contains("dead")) {
        solo.el.classList.add("dead");
        solo.status.innerHTML = "<b>timeout</b> after 10:00 · result <b>unknown</b>";
        solo.count.textContent = "bounds unchanged";
      }
      const lo = sim.lowerAt(mins);
      const hi = sim.upperAt(mins);
      setRange(swarm, lo, hi);
      if (sec > 0.6) {
        const n = sim.feed.filter((e) => e.t <= mins && e.verdict === "verified").length;
        swarm.status.innerHTML = `gap <b>${hi - lo}</b> · was ${SIM.upper0 - SIM.lower0}`;
        swarm.count.textContent = `${n} verified findings shared`;
      }
    });
  },
};

function pct(k: number) {
  return ((k - K_MIN) / (K_MAX - K_MIN)) * 100;
}
