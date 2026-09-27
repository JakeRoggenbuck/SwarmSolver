import { h } from "./lib/dom";
import { Life } from "./lib/life";
import { STAGE_H, STAGE_W, type Scene } from "./scene";
import { scenes } from "./scenes";

const app = document.getElementById("app")!;

/** A mounted scene inside a host element, scaled to fit it. */
function mountStage(host: HTMLElement, scene: Scene, fit: "width" | "contain" = "width") {
  const stage = h("div", { class: "stage", "data-scene": scene.id });
  host.append(stage);
  let life = new Life();

  const start = () => {
    life.dispose();
    stage.replaceChildren();
    life = new Life();
    scene.mount(stage, life);
    if (scene.loop) life.after(scene.loop, start);
  };

  const resize = () => {
    const parent = fit === "contain" ? host.parentElement! : host;
    const pw = parent.clientWidth;
    const ph = parent.clientHeight;
    const scale = fit === "contain" ? Math.min(pw / STAGE_W, ph / STAGE_H) : pw / STAGE_W;
    stage.style.transform = `scale(${scale})`;
    if (fit === "contain") {
      host.style.width = `${STAGE_W * scale}px`;
      host.style.height = `${STAGE_H * scale}px`;
      host.style.left = `${(pw - STAGE_W * scale) / 2}px`;
      host.style.top = `${(ph - STAGE_H * scale) / 2}px`;
    }
  };

  const ro = new ResizeObserver(resize);
  ro.observe(fit === "contain" ? host.parentElement! : host);
  resize();
  start();

  return {
    replay: start,
    destroy() {
      life.dispose();
      ro.disconnect();
      stage.remove();
    },
  };
}

/* ───────────────────────── landing page ───────────────────────── */

function landing() {
  const hero = scenes[0]!;
  const heroHost = h("div", { class: "stage-host" });
  const legend = h(
    "div",
    { class: "legend-row" },
    ...[
      ["--lead", "lead: unverified, instant"],
      ["--verified", "verified: proven by the harness"],
      ["--refuted", "refuted: counterexample found"],
      ["--wire", "transport and lower bound"],
      ["--z3", "Z3 harness"],
    ].map(([v, label]) => h("span", {}, h("i", { style: `background: var(${v})` }), label!)),
  );

  const rows = scenes.slice(1).map((scene, i) => {
    const host = h("div", { class: "stage-host" });
    host.append(h("button", { class: "stage-click", "aria-label": `Present ${scene.title}` }));
    host.querySelector(".stage-click")!.addEventListener("click", () => openPresent(scene.id));
    const row = h(
      "article",
      { class: "scene-row", id: scene.id },
      h(
        "div",
        { class: "meta" },
        h("div", { class: "num", text: `${String(i + 2).padStart(2, "0")} · ${scene.kicker}` }),
        h("h3", { text: scene.title }),
        h("p", { text: scene.blurb }),
        h(
          "div",
          { class: "row-actions" },
          h("a", { class: "btn", href: `#present/${scene.id}`, text: "Present ▸" }),
        ),
      ),
      host,
    );
    return { row, host, scene };
  });

  const root = h(
    "div",
    { class: "site" },
    h(
      "nav",
      { class: "nav" },
      h("a", { class: "wordmark", href: "#" }, h("i"), "Swarm"),
      h(
        "div",
        { class: "links" },
        h("a", { href: "#scenes", class: "hide-sm", text: "How it works" }),
        h("a", { href: "#recording", class: "hide-sm", text: "Recording" }),
        h("a", { class: "btn primary", href: `#present/${hero.id}`, text: "Present all ▸" }),
      ),
    ),
    h(
      "header",
      { class: "hero" },
      h("div", { class: "eyebrow", text: "Verified realtime communication for agent swarms" }),
      h("h1", { text: "Swarms of agents that only build on what's proven." }),
      h("p", {
        text: "Hundreds of agents work on one problem at once. They share incremental findings over a lock-free realtime log, and a Z3 harness checks each finding before any other agent builds on it.",
      }),
      h(
        "div",
        { class: "actions" },
        h("a", { class: "btn primary", href: `#present/${hero.id}`, text: "Present all scenes ▸" }),
        h("a", { class: "btn", href: "#scenes", text: "See how it works" }),
      ),
      h("div", { class: "hero-stage" }, heroHost),
      legend,
    ),
    h(
      "section",
      { class: "section-head", id: "scenes" },
      h("div", { class: "eyebrow", text: "How it works" }),
      h("h2", { text: `${scenes.length} scenes, each a 1920×1080 frame` }),
      h("p", {
        text: "Every diagram below is live and animated. Click one, or press Present, to open it full screen for recording. Colors mean the same thing in every scene.",
      }),
    ),
    h("div", { class: "scene-list" }, ...rows.map((r) => r.row)),
    h(
      "section",
      { class: "section-head", id: "recording" },
      h("div", { class: "eyebrow", text: "Recording" }),
      h("h2", { text: "Capturing the scenes for video" }),
    ),
    h(
      "div",
      { class: "tips" },
      tip("Present mode", "Open any scene with <kbd>Present</kbd>. <kbd>←</kbd> <kbd>→</kbd> or <kbd>Space</kbd> move between scenes. <kbd>R</kbd> replays the current one from the start."),
      tip("Clean frame", "The controls fade after 2 seconds without mouse movement, and the cursor hides with them. Press <kbd>H</kbd> to hide them for good, or add <code>?clean</code> to the URL."),
      tip("Full screen", "Press <kbd>F</kbd> for browser full screen. The stage is always 16:9 at 1920×1080 and letterboxes to fit, so a 1080p or 4K screen capture gets a pixel-exact frame."),
      tip("Deterministic", "Graphs and layouts use a fixed random seed, so every take looks the same. Dashboard and dedup numbers are simulations and are labeled as such on screen."),
    ),
    h("footer", { class: "footer", text: "Swarm · Go · WebSocket · Z3 · Claude" }),
  );

  app.replaceChildren(root);

  const heroStage = mountStage(heroHost, hero);
  const mounted = new Map<Element, ReturnType<typeof mountStage>>();
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        const r = rows.find((r) => r.host === e.target)!;
        if (e.isIntersecting && !mounted.has(r.host)) mounted.set(r.host, mountStage(r.host, r.scene));
        if (!e.isIntersecting && mounted.has(r.host)) {
          mounted.get(r.host)!.destroy();
          mounted.delete(r.host);
        }
      }
    },
    { rootMargin: "200px" },
  );
  rows.forEach((r) => io.observe(r.host));

  return () => {
    io.disconnect();
    heroStage.destroy();
    mounted.forEach((m) => m.destroy());
  };
}

function tip(title: string, html: string) {
  return h("div", { class: "tip" }, h("b", { text: title }), h("span", { html }));
}

/* ───────────────────────── present mode ───────────────────────── */

function openPresent(id: string) {
  location.hash = `present/${id}`;
}

function present(id: string) {
  let index = Math.max(0, scenes.findIndex((s) => s.id === id));
  const clean = new URLSearchParams(location.search).has("clean");
  const wrap = h("div", { class: `present${clean ? " clean" : ""}` });
  const host = h("div", { class: "stage-host" });
  const counter = h("b");
  const title = h("span");
  const hud = h(
    "div",
    { class: "hud" },
    counter,
    title,
    h("span", { class: "keys", html: "<kbd>←</kbd> <kbd>→</kbd> scenes · <kbd>R</kbd> replay · <kbd>H</kbd> hide · <kbd>F</kbd> full screen · <kbd>Esc</kbd> exit" }),
  );
  wrap.append(host, hud);
  app.replaceChildren(wrap);

  let current = mountStage(host, scenes[index]!, "contain");
  const sync = () => {
    counter.textContent = `${index + 1} / ${scenes.length}`;
    title.textContent = scenes[index]!.title;
  };
  sync();

  const go = (next: number) => {
    next = (next + scenes.length) % scenes.length;
    if (next === index) return;
    index = next;
    current.destroy();
    current = mountStage(host, scenes[index]!, "contain");
    history.replaceState(null, "", `${location.pathname}${location.search}#present/${scenes[index]!.id}`);
    sync();
  };

  let idleTimer: ReturnType<typeof setTimeout>;
  const wake = () => {
    wrap.classList.remove("idle");
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => wrap.classList.add("idle"), 2000);
  };
  wake();

  const onKey = (e: KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const k = e.key.toLowerCase();
    if (k === "arrowright" || k === " " || k === "pagedown") go(index + 1);
    else if (k === "arrowleft" || k === "pageup") go(index - 1);
    else if (k === "r") current.replay();
    else if (k === "h") wrap.classList.toggle("clean");
    else if (k === "f") {
      if (document.fullscreenElement) document.exitFullscreen();
      else document.documentElement.requestFullscreen();
    } else if (k === "escape" && !document.fullscreenElement) location.hash = "";
    else return;
    e.preventDefault();
  };
  window.addEventListener("keydown", onKey);
  window.addEventListener("mousemove", wake);

  return () => {
    window.removeEventListener("keydown", onKey);
    window.removeEventListener("mousemove", wake);
    clearTimeout(idleTimer);
    current.destroy();
  };
}

/* ───────────────────────── router ───────────────────────── */

let teardown: (() => void) | undefined;
let mode = "";

function route() {
  const m = location.hash.match(/^#present\/([\w-]+)/);
  const next = m ? "present" : "landing";
  // Moving between scenes in present mode only rewrites the hash; don't remount.
  if (next === "present" && mode === "present") return;
  if (next === "landing" && mode === "landing") return;
  teardown?.();
  mode = next;
  teardown = m ? present(m[1]!) : landing();
  if (!m) {
    const anchor = location.hash.slice(1);
    if (anchor) document.getElementById(anchor)?.scrollIntoView();
    else window.scrollTo(0, 0);
  }
}

window.addEventListener("hashchange", route);
route();
