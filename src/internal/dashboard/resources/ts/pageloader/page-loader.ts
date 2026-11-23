import { DomAttributeBag } from './dom-attribute-bag';
import { PageLoaderConfig } from './page-loader-config';
import { PageLoaderEventBus } from './page-loader-event-bus';
import { ReadinessProbe } from './readiness-probe';
import { SplashScreen } from './splash-screen';
import type { PageLoaderEventName, PageLoaderListener, PageLoaderOptions } from './types';

/**
 * Fluent orchestrator: configure → on → start → (ready) → hide.
 * Composition root for config, events, probe and splash.
 */
export class PageLoader {
  private config = new PageLoaderConfig();
  private readonly bus = new PageLoaderEventBus();
  private started = false;

  static create(): PageLoader {
    return new PageLoader();
  }

  configure(fn: (cfg: PageLoaderConfig) => PageLoaderConfig): this {
    this.config = fn(this.config);
    return this;
  }

  fromDataAttributes(el?: Element | null): this {
    const bag = el ? new DomAttributeBag(el) : DomAttributeBag.fromDocument();
    this.config = bag.toConfig(this.config);
    return this;
  }

  on<K extends PageLoaderEventName>(name: K, fn: PageLoaderListener<K>): this {
    this.bus.on(name, fn);
    return this;
  }

  options(): Readonly<PageLoaderOptions> {
    return this.config.snapshot();
  }

  async start(): Promise<this> {
    if (this.started) {
      return this;
    }
    this.started = true;
    const opts = this.config.snapshot();
    const splash = new SplashScreen(opts);
    const probe = new ReadinessProbe(opts);
    let revealed = false;
    const reveal = async (reason: string) => {
      if (revealed) {
        return;
      }
      revealed = true;
      this.bus.emit('showing', { fadeMs: opts.fadeMs });
      await splash.hide(opts.fadeMs);
      this.bus.emit('hidden', {});
      if (reason === 'failsafe') {
        this.bus.emit('error', { message: 'page loader failsafe reveal' });
      }
    };
    const failsafe = window.setTimeout(() => {
      void reveal('failsafe');
    }, opts.maxMs + opts.fadeMs + 50);

    splash.show('Starting…');
    this.bus.emit('loading', { reason: 'start' });
    this.bus.emit('progress', { ratio: 0.05, label: 'Starting…' });
    try {
      const { waitedMs } = await probe.wait((ratio, label) => {
        splash.progress(ratio, label);
        this.bus.emit('progress', { ratio, label });
      });
      this.bus.emit('ready', { waitedMs });
      await reveal('ready');
    } catch (cause) {
      this.bus.emit('error', {
        message: cause instanceof Error ? cause.message : 'page loader failed',
        cause,
      });
      await reveal('error');
    } finally {
      window.clearTimeout(failsafe);
    }
    return this;
  }
}
