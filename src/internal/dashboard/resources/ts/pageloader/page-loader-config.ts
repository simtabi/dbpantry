import type { PageLoaderOptions } from './types';

/** Fluent, immutable-style options builder (SOLID: single responsibility). */
export class PageLoaderConfig {
  private readonly opts: PageLoaderOptions;

  constructor(seed?: Partial<PageLoaderOptions>) {
    this.opts = {
      minMs: 140,
      maxMs: 900,
      fadeMs: 160,
      waitFonts: false,
      waitWindowLoad: false,
      readySelector: '',
      splashSelector: '[data-dbpantry-splash]',
      loadingAttr: 'data-dbpantry-loading',
      fontBudgetMs: 400,
      resourceBudgetMs: 1200,
      ...seed,
    };
  }

  withMinMs(ms: number): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, minMs: Math.max(0, ms) });
  }

  withMaxMs(ms: number): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, maxMs: Math.max(0, ms) });
  }

  withFadeMs(ms: number): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, fadeMs: Math.max(0, ms) });
  }

  withFonts(on: boolean): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, waitFonts: !!on });
  }

  withWindowLoad(on: boolean): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, waitWindowLoad: !!on });
  }

  withReadySelector(sel: string): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, readySelector: sel || '' });
  }

  withSplashSelector(sel: string): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, splashSelector: sel || '[data-dbpantry-splash]' });
  }

  withFontBudgetMs(ms: number): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, fontBudgetMs: Math.max(0, ms) });
  }

  withResourceBudgetMs(ms: number): PageLoaderConfig {
    return new PageLoaderConfig({ ...this.opts, resourceBudgetMs: Math.max(0, ms) });
  }

  snapshot(): Readonly<PageLoaderOptions> {
    return { ...this.opts };
  }
}
