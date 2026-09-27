/**
 * A scene's lifetime. Every timer, frame loop and tween goes through here so that
 * unmounting (or pressing R to replay) cancels everything at once.
 */
export class Life {
  alive = true;
  private timers = new Set<ReturnType<typeof setTimeout>>();
  private intervals = new Set<ReturnType<typeof setInterval>>();
  private frames = new Set<number>();
  private cleanups: (() => void)[] = [];

  after(ms: number, fn: () => void) {
    const id = setTimeout(() => {
      this.timers.delete(id);
      if (this.alive) fn();
    }, ms);
    this.timers.add(id);
  }

  every(ms: number, fn: () => void) {
    const id = setInterval(() => this.alive && fn(), ms);
    this.intervals.add(id);
  }

  wait(ms: number) {
    return new Promise<void>((resolve) => this.after(ms, resolve));
  }

  /** Runs fn every animation frame with elapsed seconds and delta seconds. */
  loop(fn: (t: number, dt: number) => void) {
    const start = performance.now();
    let last = start;
    const tick = (now: number) => {
      if (!this.alive) return;
      fn((now - start) / 1000, Math.min(0.1, (now - last) / 1000));
      last = now;
      const id = requestAnimationFrame(tick);
      this.frames.add(id);
    };
    this.frames.add(requestAnimationFrame(tick));
  }

  /** Tween from 0..1 over ms. Resolves when done; never resolves if the scene is disposed. */
  tween(ms: number, fn: (t: number) => void) {
    return new Promise<void>((resolve) => {
      const start = performance.now();
      const tick = (now: number) => {
        if (!this.alive) return;
        const t = Math.min(1, (now - start) / ms);
        fn(t);
        if (t < 1) this.frames.add(requestAnimationFrame(tick));
        else resolve();
      };
      this.frames.add(requestAnimationFrame(tick));
    });
  }

  onDispose(fn: () => void) {
    this.cleanups.push(fn);
  }

  dispose() {
    this.alive = false;
    this.timers.forEach(clearTimeout);
    this.intervals.forEach(clearInterval);
    this.frames.forEach(cancelAnimationFrame);
    this.cleanups.forEach((fn) => fn());
    this.timers.clear();
    this.intervals.clear();
    this.frames.clear();
    this.cleanups = [];
  }
}
