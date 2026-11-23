import type { PageLoaderOptions, ReadinessProgress } from './types';

/**
 * Sequential, budgeted readiness gates with progress labels.
 * Defaults favour a fast local reveal (DOM + content), not full resource load.
 */
export class ReadinessProbe {
  constructor(
    private readonly opts: Readonly<PageLoaderOptions>,
    private readonly doc: Document = document,
    private readonly win: Window = window,
  ) {}

  private delay(ms: number): Promise<void> {
    return new Promise((resolve) => {
      this.win.setTimeout(resolve, ms);
    });
  }

  private raceBudget(work: Promise<void>, budgetMs: number): Promise<void> {
    if (budgetMs <= 0) {
      return work;
    }
    return Promise.race([work, this.delay(budgetMs)]);
  }

  /** DOMContentLoaded or already interactive/complete — never miss the event. */
  private whenDocument(): Promise<void> {
    if (this.doc.readyState !== 'loading') {
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      let done = false;
      const finish = () => {
        if (done) {
          return;
        }
        done = true;
        this.win.clearInterval(poll);
        resolve();
      };
      this.doc.addEventListener('DOMContentLoaded', finish, { once: true });
      const poll = this.win.setInterval(() => {
        if (this.doc.readyState !== 'loading') {
          finish();
        }
      }, 16);
      this.win.setTimeout(finish, Math.min(500, Math.max(50, this.opts.maxMs)));
    });
  }

  private whenWindowLoad(): Promise<void> {
    if (!this.opts.waitWindowLoad) {
      return Promise.resolve();
    }
    if (this.doc.readyState === 'complete') {
      return Promise.resolve();
    }
    return this.raceBudget(
      new Promise((resolve) => {
        this.win.addEventListener('load', () => resolve(), { once: true });
      }),
      this.opts.resourceBudgetMs,
    );
  }

  private whenFonts(): Promise<void> {
    if (!this.opts.waitFonts) {
      return Promise.resolve();
    }
    const fonts = (this.doc as Document & { fonts?: FontFaceSet }).fonts;
    if (!fonts || !fonts.ready) {
      return Promise.resolve();
    }
    return this.raceBudget(
      fonts.ready.then(() => undefined).catch(() => undefined),
      this.opts.fontBudgetMs,
    );
  }

  private whenSelector(): Promise<void> {
    const sel = this.opts.readySelector;
    if (!sel) {
      return Promise.resolve();
    }
    if (this.doc.querySelector(sel)) {
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      const obs = new MutationObserver(() => {
        if (this.doc.querySelector(sel)) {
          obs.disconnect();
          resolve();
        }
      });
      obs.observe(this.doc.documentElement, { childList: true, subtree: true });
      this.win.setTimeout(() => {
        obs.disconnect();
        resolve();
      }, Math.min(400, Math.max(0, this.opts.maxMs)));
    });
  }

  async wait(onProgress?: ReadinessProgress): Promise<{ waitedMs: number }> {
    const started = Date.now();
    let active = true;
    const report = (ratio: number, label: string) => {
      if (active) {
        onProgress?.(ratio, label);
      }
    };

    let maxTimer = 0;
    const capped = new Promise<void>((resolve) => {
      maxTimer = this.win.setTimeout(() => {
        report(0.95, 'Opening…');
        resolve();
      }, this.opts.maxMs);
    });

    const stages = (async () => {
      report(0.05, 'Starting…');
      await this.whenDocument();
      report(0.28, 'Document ready');
      await this.whenSelector();
      report(0.52, 'Content ready');
      await this.whenFonts();
      report(this.opts.waitFonts ? 0.7 : 0.62, this.opts.waitFonts ? 'Fonts ready' : 'Layout ready');
      await this.whenWindowLoad();
      if (this.opts.waitWindowLoad) {
        report(0.82, 'Resources ready');
      }
      const elapsed = Date.now() - started;
      const remain = Math.max(0, this.opts.minMs - elapsed);
      if (remain > 0) {
        report(0.9, 'Almost ready…');
        await this.delay(remain);
      }
    })();

    try {
      await Promise.race([stages, capped]);
    } finally {
      this.win.clearTimeout(maxTimer);
      report(1, 'Ready');
      active = false;
    }
    return { waitedMs: Date.now() - started };
  }
}
