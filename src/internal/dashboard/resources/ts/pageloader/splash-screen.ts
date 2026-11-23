import type { PageLoaderOptions } from './types';

/** Presentation of the splash node bound via data-dbpantry-splash*. */
export class SplashScreen {
  private root: HTMLElement | null = null;

  constructor(
    private readonly opts: Readonly<PageLoaderOptions>,
    private readonly doc: Document = document,
  ) {
    this.resolveRoot();
  }

  /** Re-query so a head/early boot still finds the splash after parse. */
  private resolveRoot(): HTMLElement | null {
    this.root = this.doc.querySelector(this.opts.splashSelector) as HTMLElement | null;
    return this.root;
  }

  private ensureRoot(): HTMLElement | null {
    if (this.root && this.doc.contains(this.root)) {
      return this.root;
    }
    return this.resolveRoot();
  }

  exists(): boolean {
    return !!this.ensureRoot();
  }

  show(label?: string): this {
    const root = this.ensureRoot();
    if (!root) {
      this.doc.documentElement.setAttribute(this.opts.loadingAttr, '1');
      return this;
    }
    root.setAttribute('data-dbpantry-splash-state', 'visible');
    root.setAttribute('aria-hidden', 'false');
    root.setAttribute('aria-busy', 'true');
    if (label) {
      this.progress(0.05, label);
    }
    this.doc.documentElement.setAttribute(this.opts.loadingAttr, '1');
    return this;
  }

  /** Determinate percentage + status copy on the splash. */
  progress(ratio: number, label?: string): this {
    const root = this.ensureRoot();
    if (!root) {
      return this;
    }
    const pct = Math.max(0, Math.min(100, Math.round(ratio * 100)));
    if (label) {
      const el = root.querySelector('[data-dbpantry-splash-label]');
      if (el) {
        el.textContent = label;
      }
    }
    const pctEl = root.querySelector('[data-dbpantry-splash-pct]');
    if (pctEl) {
      pctEl.textContent = `${pct}%`;
    }
    const track = root.querySelector('[data-dbpantry-splash-progress]');
    if (track) {
      track.removeAttribute('data-indeterminate');
      track.setAttribute('aria-valuenow', String(pct));
      if (label) {
        track.setAttribute('aria-valuetext', `${pct}% — ${label}`);
      } else {
        track.setAttribute('aria-valuetext', `${pct}%`);
      }
      const bar =
        (track.querySelector('[data-dbpantry-splash-bar]') as HTMLElement | null) ||
        (track.querySelector('span') as HTMLElement | null);
      if (bar) {
        bar.style.width = `${pct}%`;
        bar.style.transform = 'none';
      }
    }
    return this;
  }

  async hide(fadeMs: number): Promise<this> {
    const root = this.ensureRoot();
    if (!root) {
      this.doc.documentElement.removeAttribute(this.opts.loadingAttr);
      return this;
    }
    root.setAttribute('data-dbpantry-splash-state', 'hiding');
    root.setAttribute('aria-busy', 'false');
    await new Promise<void>((resolve) => {
      window.setTimeout(resolve, fadeMs);
    });
    root.setAttribute('data-dbpantry-splash-state', 'hidden');
    root.setAttribute('aria-hidden', 'true');
    this.doc.documentElement.removeAttribute(this.opts.loadingAttr);
    return this;
  }
}
