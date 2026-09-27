import { h, rng } from "../lib/dom";
import type { Scene } from "../scene";
import { reveal } from "../scene";

const LEAD = "#ffb547";
const VERIFIED = "#3ddc97";

export const title: Scene = {
  id: "title",
  kicker: "Swarm",
  title: "Swarm",
  blurb: "Title card. Agents drift as a live network, and findings pulse between them.",
  mount(root, life) {
    const canvas = h("canvas", { width: 3840, height: 2160, class: "net" });
    root.append(canvas);
    const ctx = canvas.getContext("2d")!;
    ctx.scale(2, 2);

    const r = rng(7);
    const N = 90;
    const nodes = Array.from({ length: N }, () => ({
      x: r() * 1920,
      y: r() * 1080,
      vx: (r() - 0.5) * 18,
      vy: (r() - 0.5) * 18,
    }));
    type Pulse = { a: number; b: number; t: number; color: string };
    let pulses: Pulse[] = [];
    const LINK = 190;

    life.loop((_, dt) => {
      ctx.clearRect(0, 0, 1920, 1080);
      for (const n of nodes) {
        n.x += n.vx * dt;
        n.y += n.vy * dt;
        if (n.x < -40) n.x = 1960;
        if (n.x > 1960) n.x = -40;
        if (n.y < -40) n.y = 1120;
        if (n.y > 1120) n.y = -40;
      }
      const links: [number, number][] = [];
      for (let i = 0; i < N; i++)
        for (let j = i + 1; j < N; j++) {
          const dx = nodes[i]!.x - nodes[j]!.x;
          const dy = nodes[i]!.y - nodes[j]!.y;
          const d = Math.hypot(dx, dy);
          if (d < LINK) {
            links.push([i, j]);
            ctx.strokeStyle = `rgba(139,149,168,${0.22 * (1 - d / LINK)})`;
            ctx.lineWidth = 1.2;
            ctx.beginPath();
            ctx.moveTo(nodes[i]!.x, nodes[i]!.y);
            ctx.lineTo(nodes[j]!.x, nodes[j]!.y);
            ctx.stroke();
          }
        }
      if (links.length && Math.random() < dt * 5) {
        const [a, b] = links[Math.floor(Math.random() * links.length)]!;
        pulses.push({ a, b, t: 0, color: Math.random() < 0.55 ? VERIFIED : LEAD });
      }
      pulses = pulses.filter((p) => (p.t += dt * 1.1) < 1);
      for (const p of pulses) {
        const A = nodes[p.a]!;
        const B = nodes[p.b]!;
        const x = A.x + (B.x - A.x) * p.t;
        const y = A.y + (B.y - A.y) * p.t;
        ctx.fillStyle = p.color;
        ctx.shadowColor = p.color;
        ctx.shadowBlur = 16;
        ctx.beginPath();
        ctx.arc(x, y, 4.5, 0, Math.PI * 2);
        ctx.fill();
        ctx.shadowBlur = 0;
      }
      for (const n of nodes) {
        ctx.fillStyle = "#c3cad8";
        ctx.beginPath();
        ctx.arc(n.x, n.y, 3.2, 0, Math.PI * 2);
        ctx.fill();
      }
    });

    const block = h(
      "div",
      { class: "title-block" },
      h("div", { class: "title-kicker rv", text: "Verified realtime communication for agent swarms" }),
      h("div", { class: "title-word rv" }, h("i"), "Swarm"),
      h("div", {
        class: "title-tag rv",
        html: "Hundreds of agents. One shared log.<br/>Every finding <b>proven</b> before anyone builds on it.",
      }),
    );
    root.append(h("div", { class: "title-vignette" }), block);
    block.querySelectorAll(".rv").forEach((el, i) => reveal(life, el, 300 + i * 450));
  },
};
