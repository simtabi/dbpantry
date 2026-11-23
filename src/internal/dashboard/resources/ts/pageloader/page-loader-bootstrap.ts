import { PageLoader } from './page-loader';

/**
 * Auto-wires PageLoader from data-dbpantry-* on the document.
 * Disabled with data-dbpantry-loader="off".
 * Defers until the document has a body so the splash node exists.
 */
export class PageLoaderBootstrap {
  constructor(
    private readonly doc: Document = document,
    private readonly win: Window & { dbpantryPageLoader?: PageLoader } = window as Window & {
      dbpantryPageLoader?: PageLoader;
    },
  ) {}

  shouldRun(): boolean {
    const host = this.doc.querySelector('[data-dbpantry-loader]');
    if (!host) {
      return !!this.doc.querySelector('[data-dbpantry-splash]');
    }
    const v = host.getAttribute('data-dbpantry-loader');
    return !(v === 'off' || v === '0' || v === 'false');
  }

  boot(): PageLoader | null {
    if (!this.shouldRun()) {
      return null;
    }
    const start = () => {
      const loader = PageLoader.create().fromDataAttributes();
      this.win.dbpantryPageLoader = loader;
      void loader.start();
      return loader;
    };
    if (this.doc.readyState === 'loading' || !this.doc.querySelector('[data-dbpantry-splash]')) {
      let started: PageLoader | null = null;
      const run = () => {
        if (started) {
          return;
        }
        started = start();
      };
      this.doc.addEventListener('DOMContentLoaded', run, { once: true });
      // Failsafe if DOMContentLoaded already raced past (parsed but splash late).
      this.win.setTimeout(run, 0);
      return started;
    }
    return start();
  }

  static auto(): PageLoader | null {
    return new PageLoaderBootstrap().boot();
  }
}
