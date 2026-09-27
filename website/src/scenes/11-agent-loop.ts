import { h, s } from "../lib/dom";
import { svgRoot } from "../lib/svgkit";
import type { Scene } from "../scene";
import { frame } from "../scene";

const CX = 840;
const CY = 345;
const RX = 560;
const RY = 262;

const STEPS = [
  ["Sync", "Replay the log from seq 0, apply the snapshot, keep a local working graph"],
  ["Digest", "Current bounds, a focus region, new separations there, recent refutations"],
  ["Plan", "Claude picks a move: a dense subgraph, a separation, or how to steer the search"],
  ["Search", "DSatur or tabucol, run locally in Go and seeded by the plan"],
  ["Publish", "Optional local Z3 pre-check, then post the claim to the leads lane"],
];

export const agentLoop: Scene = {
  id: "agent-loop",
  kicker: "The agent",
  title: "Each agent runs one loop",
  blurb:
    "Agents are Go programs using anthropic-sdk-go. Mostly Haiku workers, plus a few Sonnet and Opus strategists. Each turn the agent reads a compact digest instead of the whole history, lets the model choose a move, runs a fast local heuristic, and publishes what it finds.",
  mount(root, life) {
    const content = frame(root, this);
    const svg = svgRoot(1680, 690);
    content.append(svg);
    s("ellipse", { cx: CX, cy: CY, rx: RX, ry: RY, class: "al-track" }, svg);
    const dot = s("circle", { r: 13, fill: "#5aa9ff", filter: "url(#glow)" }, svg);

    const angles = STEPS.map((_, i) => -90 + i * 72);
    const cards = STEPS.map(([name, body], i) => {
      const a = (angles[i]! * Math.PI) / 180;
      const x = CX + RX * Math.cos(a);
      const y = CY + RY * Math.sin(a);
      const card = h(
        "div",
        { class: "al-card" },
        h("div", { class: "al-num mono", text: String(i + 1).padStart(2, "0") }),
        h("div", {}, h("b", { text: name! }), h("p", { text: body! })),
      );
      card.style.left = `${x}px`;
      card.style.top = `${y}px`;
      content.append(card);
      return card;
    });

    content.append(
      h(
        "div",
        { class: "al-center" },
        h("div", { class: "al-center-title", text: "agent loop" }),
        h("div", { class: "al-center-sub mono", text: "Go · anthropic-sdk-go" }),
        h(
          "div",
          { class: "al-models" },
          h("span", { class: "tag", text: "Haiku workers" }),
          h("span", { class: "tag strong", text: "Sonnet / Opus strategists" }),
        ),
      ),
    );

    const PERIOD = 11; // seconds per lap
    life.loop((t) => {
      const deg = -90 + ((t / PERIOD) * 360) % 360;
      const a = (deg * Math.PI) / 180;
      dot.setAttribute("cx", String(CX + RX * Math.cos(a)));
      dot.setAttribute("cy", String(CY + RY * Math.sin(a)));
      // the step whose position the dot most recently passed is active
      const rel = (deg + 90 + 360) % 360;
      const active = Math.floor((rel + 10) / 72) % 5;
      cards.forEach((c, i) => c.classList.toggle("active", i === active));
    });
  },
};
