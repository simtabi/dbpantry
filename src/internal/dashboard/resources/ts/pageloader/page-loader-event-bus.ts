import type { PageLoaderEventMap, PageLoaderEventName, PageLoaderListener } from './types';

/** Tiny typed pub/sub for loader lifecycle events. */
export class PageLoaderEventBus {
  private readonly listeners = new Map<PageLoaderEventName, Set<PageLoaderListener<PageLoaderEventName>>>();

  on<K extends PageLoaderEventName>(name: K, fn: PageLoaderListener<K>): this {
    const set = this.listeners.get(name) ?? new Set();
    set.add(fn as PageLoaderListener<PageLoaderEventName>);
    this.listeners.set(name, set);
    return this;
  }

  off<K extends PageLoaderEventName>(name: K, fn: PageLoaderListener<K>): this {
    this.listeners.get(name)?.delete(fn as PageLoaderListener<PageLoaderEventName>);
    return this;
  }

  emit<K extends PageLoaderEventName>(name: K, detail: PageLoaderEventMap[K]): this {
    const set = this.listeners.get(name);
    if (!set) {
      return this;
    }
    set.forEach((fn) => {
      try {
        (fn as PageLoaderListener<K>)(detail);
      } catch {
        /* listener faults must not break reveal */
      }
    });
    return this;
  }

  clear(): this {
    this.listeners.clear();
    return this;
  }
}
