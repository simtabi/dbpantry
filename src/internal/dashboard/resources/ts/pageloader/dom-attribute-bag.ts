import { PageLoaderConfig } from './page-loader-config';

/**
 * Reads configuration exclusively from data-dbpantry-* attributes
 * (KISS: one bag, one prefix).
 */
export class DomAttributeBag {
  constructor(
    private readonly el: Element | null,
    private readonly prefix = 'data-dbpantry-',
  ) {}

  static fromDocument(doc: Document = document): DomAttributeBag {
    const host =
      doc.querySelector('[data-dbpantry-loader]') ||
      doc.documentElement;
    return new DomAttributeBag(host);
  }

  private raw(name: string): string | null {
    if (!this.el) {
      return null;
    }
    return this.el.getAttribute(this.prefix + name);
  }

  private bool(name: string, fallback: boolean): boolean {
    const v = this.raw(name);
    if (v == null || v === '') {
      return fallback;
    }
    return !(v === '0' || v === 'false' || v === 'off' || v === 'no');
  }

  private int(name: string, fallback: number): number {
    const v = this.raw(name);
    if (v == null || v === '') {
      return fallback;
    }
    const n = Number(v);
    return Number.isFinite(n) ? n : fallback;
  }

  toConfig(base = new PageLoaderConfig()): PageLoaderConfig {
    const snap = base.snapshot();
    let cfg = base
      .withMinMs(this.int('min-ms', snap.minMs))
      .withMaxMs(this.int('max-ms', snap.maxMs))
      .withFadeMs(this.int('fade-ms', snap.fadeMs))
      .withFonts(this.bool('wait-fonts', snap.waitFonts))
      .withWindowLoad(this.bool('wait-window-load', snap.waitWindowLoad))
      .withFontBudgetMs(this.int('font-budget-ms', snap.fontBudgetMs))
      .withResourceBudgetMs(this.int('resource-budget-ms', snap.resourceBudgetMs));
    const ready = this.raw('ready-selector');
    if (ready) {
      cfg = cfg.withReadySelector(ready);
    }
    const splash = this.raw('splash-selector');
    if (splash) {
      cfg = cfg.withSplashSelector(splash);
    }
    return cfg;
  }
}
