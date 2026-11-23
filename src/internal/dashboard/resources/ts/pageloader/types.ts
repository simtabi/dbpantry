/** Shared contracts for the page loader (DDD: domain vocabulary). */

export type PageLoaderEventName =
  | 'loading'
  | 'progress'
  | 'ready'
  | 'showing'
  | 'hidden'
  | 'error';

export type PageLoaderEventMap = {
  loading: { reason: string };
  progress: { ratio: number; label?: string };
  ready: { waitedMs: number };
  showing: { fadeMs: number };
  hidden: Record<string, never>;
  error: { message: string; cause?: unknown };
};

export type PageLoaderListener<K extends PageLoaderEventName> = (
  detail: PageLoaderEventMap[K],
) => void;

/** Stage callback while the readiness probe advances (0..1). */
export type ReadinessProgress = (ratio: number, label: string) => void;

export interface PageLoaderOptions {
  /** Minimum splash time so the brand does not flash. */
  minMs: number;
  /** Hard cap; hide even if readiness never resolves. */
  maxMs: number;
  /** CSS fade duration after ready. */
  fadeMs: number;
  /** Wait for document.fonts.ready when available (budgeted). */
  waitFonts: boolean;
  /** Wait for window "load" (all subresources). Prefer off for local UX. */
  waitWindowLoad: boolean;
  /** Optional CSS selector that must exist before reveal. */
  readySelector: string;
  /** Splash root selector (data-dbpantry-splash). */
  splashSelector: string;
  /** Document attribute flipped while loading. */
  loadingAttr: string;
  /** Soft budget for fonts.ready before continuing. */
  fontBudgetMs: number;
  /** Soft budget for window load when waitWindowLoad is on. */
  resourceBudgetMs: number;
}
