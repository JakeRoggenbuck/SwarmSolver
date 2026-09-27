import { h } from "./lib/dom";
import type { Life } from "./lib/life";

/** Every scene draws on a fixed 1920×1080 stage, so it looks identical at any window size. */
export const STAGE_W = 1920;
export const STAGE_H = 1080;

/** The content box under the header, in stage pixels. Scenes lay out inside it. */
export const CONTENT = { x: 120, y: 290, w: 1680, h: 690 };

export interface Scene {
  id: string;
  kicker: string;
  title: string;
  /** One or two sentences for the website and for your voiceover notes. */
  blurb: string;
  /** If set, the scene restarts itself after this many ms (for one-shot timelines). */
  loop?: number;
  mount(root: HTMLElement, life: Life): void;
}

/** Standard header (kicker + title) and a content box. Returns the content box. */
export function frame(root: HTMLElement, scene: Pick<Scene, "kicker" | "title">) {
  root.append(
    h("div", { class: "kicker", text: scene.kicker }),
    h("h2", { class: "title", text: scene.title }),
  );
  const content = h("div", { class: "content" });
  root.append(content);
  return content;
}

/** Add a class after a delay, for staggered reveals driven by CSS transitions. */
export function reveal(life: Life, el: Element, ms: number, cls = "in") {
  life.after(ms, () => el.classList.add(cls));
}
