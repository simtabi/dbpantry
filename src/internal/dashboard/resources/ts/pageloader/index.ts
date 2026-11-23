export { DomAttributeBag } from './dom-attribute-bag';
export { PageLoaderBootstrap } from './page-loader-bootstrap';
export { PageLoaderConfig } from './page-loader-config';
export { PageLoaderEventBus } from './page-loader-event-bus';
export { PageLoader } from './page-loader';
export { ReadinessProbe } from './readiness-probe';
export { SplashScreen } from './splash-screen';
export type {
  PageLoaderEventMap,
  PageLoaderEventName,
  PageLoaderListener,
  PageLoaderOptions,
  ReadinessProgress,
} from './types';

import { PageLoaderBootstrap } from './page-loader-bootstrap';

PageLoaderBootstrap.auto();
