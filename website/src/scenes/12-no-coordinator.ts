import { h, rng, fmtHash } from "../lib/dom";
import type { Scene } from "../scene";
import { frame } from "../scene";

const STRATEGIES = ["dense cores", "big classes", "hub seps", "restarts", "recolor tails", "clique hunt"];
const COLS = 6;
const ROWS = 4;
const CARD_W = 158;
const CARD_H = 118;
const GAP = 16;
const DEDUP = { x: 1130, y: 60, w: 550, h: 170 };

export const noCoordinator: Scene = {
  id: "no-coordinator",
  kicker: "No central coordinator",
  title: "Overlap is a feature, and it's cheap",
  blurb:
    "No one assigns work. Each agent draws its own random seed, strategy prompt, focus region, model and temperature, so overlapping agents explore the same area in different ways. When two agents find the same claim it hashes to the same id, so the verifier checks it once and drops the copy.",
  mount(root, life) {
    const content = frame(root, this);
    const r = rng(9);

    const grid = h("div", { class: "nc-grid" });
    const cards = Array.from({ length: COLS * ROWS }, (_, i) => {
      const model = r() < 0.72 ? "haiku" : r() < 0.7 ? "sonnet" : "opus";
      const card = h(
        "div",
        { class: "nc-card" },
        h("div", { class: "nc-top" }, h("b", { class: "mono", text: `a${String(i + 1).padStart(2, "0")}` }), h("span", { class: `nc-model ${model}`, text: model })),
        h("div", { class: "nc-strat", text: STRATEGIES[Math.floor(r() * STRATEGIES.length)]! }),
        h("div", { class: "nc-seed mono", text: `seed ${fmtHash(r, 2)} · T${(0.3 + r() * 0.7).toFixed(1)}` }),
      );
      card.style.left = `${(i % COLS) * (CARD_W + GAP)}px`;
      card.style.top = `${Math.floor(i / COLS) * (CARD_H + GAP) + 40}px`;
      grid.append(card);
      return card;
    });
    content.append(grid);

    const dedup = h(
      "div",
      { class: "nc-dedup" },
      h("b", { text: "id = sha256(normalized claim)" }),
      h("span", { class: "mono", text: "seen before? → drop" }),
    );
    const idText = h("div", { class: "nc-id mono", text: "" });
    dedup.append(idText);
    content.append(dedup);

    const stat = (label: string, cls: string) => {
      const v = h("b", { class: "mono", text: "0" });
      content.querySelector(".nc-stats")!.append(h("div", { class: `nc-stat ${cls}` }, v, h("span", { text: label })));
      return v;
    };
    content.append(h("div", { class: "nc-stats" }));
    const sPub = stat("claims published", "");
    const sDup = stat("duplicates dropped", "dup");
    const sZ3 = stat("verifier checks", "z3");
    content.append(h("div", { class: "sim-note nc-note", text: "live simulation" }));

    // A small pool of possible claims with a skewed distribution, so agents collide often.
    const POOL = Array.from({ length: 40 }, () => fmtHash(r, 4));
    const seen = new Set<string>();
    let pub = 0;
    let dup = 0;

    const emit = () => {
      const i = Math.floor(r() * cards.length);
      const card = cards[i]!;
      const id = POOL[Math.floor(Math.pow(r(), 2.2) * POOL.length)]!;
      card.classList.remove("flash");
      void card.offsetWidth;
      card.classList.add("flash");

      const x0 = (i % COLS) * (CARD_W + GAP) + CARD_W / 2;
      const y0 = Math.floor(i / COLS) * (CARD_H + GAP) + 40 + CARD_H / 2;
      const x1 = DEDUP.x + 40;
      const y1 = DEDUP.y + DEDUP.h / 2;
      const p = h("i", { class: "nc-packet" });
      content.append(p);
      life.onDispose(() => p.remove());
      p.animate(
        [
          { transform: `translate(${x0}px, ${y0}px)` },
          { transform: `translate(${(x0 + x1) / 2}px, ${Math.min(y0, y1) - 40}px)`, offset: 0.5 },
          { transform: `translate(${x1}px, ${y1}px)` },
        ],
        { duration: 700, easing: "ease-in-out" },
      ).finished.then(() => {
        p.remove();
        if (!life.alive) return;
        pub++;
        sPub.textContent = String(pub);
        const isDup = seen.has(id);
        idText.textContent = `sha256:${id}… ${isDup ? "✕ duplicate" : "✓ new → Z3"}`;
        idText.className = `nc-id mono ${isDup ? "dup" : "new"}`;
        if (isDup) {
          dup++;
          sDup.textContent = String(dup);
        } else {
          seen.add(id);
          sZ3.textContent = String(seen.size);
        }
      });
    };

    life.every(260, emit);
  },
};
