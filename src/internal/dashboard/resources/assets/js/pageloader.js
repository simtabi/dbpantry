// Built from resources/ts/pageloader (7 fluent classes). Keep in sync when editing TS.
// Classes: PageLoaderConfig, PageLoaderEventBus, DomAttributeBag, ReadinessProbe,
// SplashScreen, PageLoader, PageLoaderBootstrap.
(function (global) {
  'use strict';

  function PageLoaderConfig(seed) {
    this.opts = Object.assign({
      minMs: 140,
      maxMs: 900,
      fadeMs: 160,
      waitFonts: false,
      waitWindowLoad: false,
      readySelector: '',
      splashSelector: '[data-dbpantry-splash]',
      loadingAttr: 'data-dbpantry-loading',
      fontBudgetMs: 400,
      resourceBudgetMs: 1200
    }, seed || {});
  }
  PageLoaderConfig.prototype.withMinMs = function (ms) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { minMs: Math.max(0, ms) }));
  };
  PageLoaderConfig.prototype.withMaxMs = function (ms) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { maxMs: Math.max(0, ms) }));
  };
  PageLoaderConfig.prototype.withFadeMs = function (ms) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { fadeMs: Math.max(0, ms) }));
  };
  PageLoaderConfig.prototype.withFonts = function (on) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { waitFonts: !!on }));
  };
  PageLoaderConfig.prototype.withWindowLoad = function (on) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { waitWindowLoad: !!on }));
  };
  PageLoaderConfig.prototype.withReadySelector = function (sel) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { readySelector: sel || '' }));
  };
  PageLoaderConfig.prototype.withSplashSelector = function (sel) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { splashSelector: sel || '[data-dbpantry-splash]' }));
  };
  PageLoaderConfig.prototype.withFontBudgetMs = function (ms) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { fontBudgetMs: Math.max(0, ms) }));
  };
  PageLoaderConfig.prototype.withResourceBudgetMs = function (ms) {
    return new PageLoaderConfig(Object.assign({}, this.opts, { resourceBudgetMs: Math.max(0, ms) }));
  };
  PageLoaderConfig.prototype.snapshot = function () {
    return Object.assign({}, this.opts);
  };

  function PageLoaderEventBus() {
    this.listeners = {};
  }
  PageLoaderEventBus.prototype.on = function (name, fn) {
    (this.listeners[name] || (this.listeners[name] = [])).push(fn);
    return this;
  };
  PageLoaderEventBus.prototype.off = function (name, fn) {
    var list = this.listeners[name] || [];
    this.listeners[name] = list.filter(function (x) { return x !== fn; });
    return this;
  };
  PageLoaderEventBus.prototype.emit = function (name, detail) {
    (this.listeners[name] || []).forEach(function (fn) {
      try { fn(detail); } catch (err) { /* ignore */ }
    });
    return this;
  };
  PageLoaderEventBus.prototype.clear = function () {
    this.listeners = {};
    return this;
  };

  function DomAttributeBag(el, prefix) {
    this.el = el;
    this.prefix = prefix || 'data-dbpantry-';
  }
  DomAttributeBag.fromDocument = function (doc) {
    doc = doc || document;
    var host = doc.querySelector('[data-dbpantry-loader]') || doc.documentElement;
    return new DomAttributeBag(host);
  };
  DomAttributeBag.prototype.raw = function (name) {
    return this.el ? this.el.getAttribute(this.prefix + name) : null;
  };
  DomAttributeBag.prototype.bool = function (name, fallback) {
    var v = this.raw(name);
    if (v == null || v === '') { return fallback; }
    return !(v === '0' || v === 'false' || v === 'off' || v === 'no');
  };
  DomAttributeBag.prototype.int = function (name, fallback) {
    var v = this.raw(name);
    if (v == null || v === '') { return fallback; }
    var n = Number(v);
    return isFinite(n) ? n : fallback;
  };
  DomAttributeBag.prototype.toConfig = function (base) {
    base = base || new PageLoaderConfig();
    var snap = base.snapshot();
    var cfg = base
      .withMinMs(this.int('min-ms', snap.minMs))
      .withMaxMs(this.int('max-ms', snap.maxMs))
      .withFadeMs(this.int('fade-ms', snap.fadeMs))
      .withFonts(this.bool('wait-fonts', snap.waitFonts))
      .withWindowLoad(this.bool('wait-window-load', snap.waitWindowLoad))
      .withFontBudgetMs(this.int('font-budget-ms', snap.fontBudgetMs))
      .withResourceBudgetMs(this.int('resource-budget-ms', snap.resourceBudgetMs));
    var ready = this.raw('ready-selector');
    if (ready) { cfg = cfg.withReadySelector(ready); }
    var splash = this.raw('splash-selector');
    if (splash) { cfg = cfg.withSplashSelector(splash); }
    return cfg;
  };

  function ReadinessProbe(opts, doc, win) {
    this.opts = opts;
    this.doc = doc || document;
    this.win = win || window;
  }
  ReadinessProbe.prototype.delay = function (ms) {
    var win = this.win;
    return new Promise(function (resolve) { win.setTimeout(resolve, ms); });
  };
  ReadinessProbe.prototype.raceBudget = function (work, budgetMs) {
    if (budgetMs <= 0) { return work; }
    return Promise.race([work, this.delay(budgetMs)]);
  };
  ReadinessProbe.prototype.whenDocument = function () {
    var self = this;
    if (this.doc.readyState !== 'loading') { return Promise.resolve(); }
    return new Promise(function (resolve) {
      var done = false;
      var poll;
      function finish() {
        if (done) { return; }
        done = true;
        self.win.clearInterval(poll);
        resolve();
      }
      self.doc.addEventListener('DOMContentLoaded', finish, { once: true });
      poll = self.win.setInterval(function () {
        if (self.doc.readyState !== 'loading') { finish(); }
      }, 16);
      self.win.setTimeout(finish, Math.min(500, Math.max(50, self.opts.maxMs)));
    });
  };
  ReadinessProbe.prototype.whenWindowLoad = function () {
    var self = this;
    if (!this.opts.waitWindowLoad) { return Promise.resolve(); }
    if (this.doc.readyState === 'complete') { return Promise.resolve(); }
    return this.raceBudget(new Promise(function (resolve) {
      self.win.addEventListener('load', function () { resolve(); }, { once: true });
    }), this.opts.resourceBudgetMs);
  };
  ReadinessProbe.prototype.whenFonts = function () {
    if (!this.opts.waitFonts || !this.doc.fonts || !this.doc.fonts.ready) {
      return Promise.resolve();
    }
    return this.raceBudget(
      this.doc.fonts.ready.then(function () {}).catch(function () {}),
      this.opts.fontBudgetMs
    );
  };
  ReadinessProbe.prototype.whenSelector = function () {
    var self = this;
    var sel = this.opts.readySelector;
    if (!sel) { return Promise.resolve(); }
    if (this.doc.querySelector(sel)) { return Promise.resolve(); }
    return new Promise(function (resolve) {
      var obs = new MutationObserver(function () {
        if (self.doc.querySelector(sel)) {
          obs.disconnect();
          resolve();
        }
      });
      obs.observe(self.doc.documentElement, { childList: true, subtree: true });
      self.win.setTimeout(function () {
        obs.disconnect();
        resolve();
      }, Math.min(400, Math.max(0, self.opts.maxMs)));
    });
  };
  ReadinessProbe.prototype.wait = function (onProgress) {
    var self = this;
    var started = Date.now();
    var active = true;
    function report(ratio, label) {
      if (active && onProgress) { onProgress(ratio, label); }
    }
    var maxTimer = 0;
    var capped = new Promise(function (resolve) {
      maxTimer = self.win.setTimeout(function () {
        report(0.95, 'Opening…');
        resolve();
      }, self.opts.maxMs);
    });
    var stages = (async function () {
      report(0.05, 'Starting…');
      await self.whenDocument();
      report(0.28, 'Document ready');
      await self.whenSelector();
      report(0.52, 'Content ready');
      await self.whenFonts();
      report(self.opts.waitFonts ? 0.7 : 0.62, self.opts.waitFonts ? 'Fonts ready' : 'Layout ready');
      await self.whenWindowLoad();
      if (self.opts.waitWindowLoad) { report(0.82, 'Resources ready'); }
      var elapsed = Date.now() - started;
      var remain = Math.max(0, self.opts.minMs - elapsed);
      if (remain > 0) {
        report(0.9, 'Almost ready…');
        await self.delay(remain);
      }
    })();
    return Promise.race([stages, capped]).then(function () {
      self.win.clearTimeout(maxTimer);
      report(1, 'Ready');
      active = false;
      return { waitedMs: Date.now() - started };
    }, function () {
      self.win.clearTimeout(maxTimer);
      report(1, 'Ready');
      active = false;
      return { waitedMs: Date.now() - started };
    });
  };

  function SplashScreen(opts, doc) {
    this.opts = opts;
    this.doc = doc || document;
    this.root = null;
    this.resolveRoot();
  }
  SplashScreen.prototype.resolveRoot = function () {
    this.root = this.doc.querySelector(this.opts.splashSelector);
    return this.root;
  };
  SplashScreen.prototype.ensureRoot = function () {
    if (this.root && this.doc.contains(this.root)) { return this.root; }
    return this.resolveRoot();
  };
  SplashScreen.prototype.exists = function () { return !!this.ensureRoot(); };
  SplashScreen.prototype.show = function (label) {
    var root = this.ensureRoot();
    if (!root) {
      this.doc.documentElement.setAttribute(this.opts.loadingAttr, '1');
      return this;
    }
    root.setAttribute('data-dbpantry-splash-state', 'visible');
    root.setAttribute('aria-hidden', 'false');
    root.setAttribute('aria-busy', 'true');
    if (label) { this.progress(0.05, label); }
    this.doc.documentElement.setAttribute(this.opts.loadingAttr, '1');
    return this;
  };
  SplashScreen.prototype.progress = function (ratio, label) {
    var root = this.ensureRoot();
    if (!root) { return this; }
    var pct = Math.max(0, Math.min(100, Math.round(ratio * 100)));
    if (label) {
      var el = root.querySelector('[data-dbpantry-splash-label]');
      if (el) { el.textContent = label; }
    }
    var pctEl = root.querySelector('[data-dbpantry-splash-pct]');
    if (pctEl) { pctEl.textContent = pct + '%'; }
    var track = root.querySelector('[data-dbpantry-splash-progress]');
    if (track) {
      track.removeAttribute('data-indeterminate');
      track.setAttribute('aria-valuenow', String(pct));
      track.setAttribute('aria-valuetext', label ? (pct + '% — ' + label) : (pct + '%'));
      var bar = track.querySelector('[data-dbpantry-splash-bar]') || track.querySelector('span');
      if (bar) {
        bar.style.width = pct + '%';
        bar.style.transform = 'none';
      }
    }
    return this;
  };
  SplashScreen.prototype.hide = function (fadeMs) {
    var self = this;
    var root = this.ensureRoot();
    if (!root) {
      this.doc.documentElement.removeAttribute(this.opts.loadingAttr);
      return Promise.resolve(this);
    }
    root.setAttribute('data-dbpantry-splash-state', 'hiding');
    root.setAttribute('aria-busy', 'false');
    return new Promise(function (resolve) {
      window.setTimeout(resolve, fadeMs);
    }).then(function () {
      root.setAttribute('data-dbpantry-splash-state', 'hidden');
      root.setAttribute('aria-hidden', 'true');
      self.doc.documentElement.removeAttribute(self.opts.loadingAttr);
      return self;
    });
  };

  function PageLoader() {
    this.config = new PageLoaderConfig();
    this.bus = new PageLoaderEventBus();
    this.started = false;
  }
  PageLoader.create = function () { return new PageLoader(); };
  PageLoader.prototype.configure = function (fn) {
    this.config = fn(this.config);
    return this;
  };
  PageLoader.prototype.fromDataAttributes = function (el) {
    var bag = el ? new DomAttributeBag(el) : DomAttributeBag.fromDocument();
    this.config = bag.toConfig(this.config);
    return this;
  };
  PageLoader.prototype.on = function (name, fn) {
    this.bus.on(name, fn);
    return this;
  };
  PageLoader.prototype.options = function () { return this.config.snapshot(); };
  PageLoader.prototype.start = function () {
    var self = this;
    if (this.started) { return Promise.resolve(this); }
    this.started = true;
    var opts = this.config.snapshot();
    var splash = new SplashScreen(opts);
    var probe = new ReadinessProbe(opts);
    var revealed = false;
    function reveal(reason) {
      if (revealed) { return Promise.resolve(); }
      revealed = true;
      self.bus.emit('showing', { fadeMs: opts.fadeMs });
      return splash.hide(opts.fadeMs).then(function () {
        self.bus.emit('hidden', {});
        if (reason === 'failsafe') {
          self.bus.emit('error', { message: 'page loader failsafe reveal' });
        }
      });
    }
    var failsafe = window.setTimeout(function () {
      reveal('failsafe');
    }, opts.maxMs + opts.fadeMs + 50);
    splash.show('Starting…');
    this.bus.emit('loading', { reason: 'start' });
    this.bus.emit('progress', { ratio: 0.05, label: 'Starting…' });
    return probe.wait(function (ratio, label) {
      splash.progress(ratio, label);
      self.bus.emit('progress', { ratio: ratio, label: label });
    }).then(function (r) {
      self.bus.emit('ready', { waitedMs: r.waitedMs });
      return reveal('ready');
    }).catch(function (cause) {
      self.bus.emit('error', {
        message: cause && cause.message ? cause.message : 'page loader failed',
        cause: cause
      });
      return reveal('error');
    }).then(function () {
      window.clearTimeout(failsafe);
      return self;
    });
  };

  function PageLoaderBootstrap(doc, win) {
    this.doc = doc || document;
    this.win = win || window;
  }
  PageLoaderBootstrap.prototype.shouldRun = function () {
    var host = this.doc.querySelector('[data-dbpantry-loader]');
    if (!host) { return !!this.doc.querySelector('[data-dbpantry-splash]'); }
    var v = host.getAttribute('data-dbpantry-loader');
    return !(v === 'off' || v === '0' || v === 'false');
  };
  PageLoaderBootstrap.prototype.boot = function () {
    var self = this;
    if (!this.shouldRun()) { return null; }
    function start() {
      var loader = PageLoader.create().fromDataAttributes();
      self.win.dbpantryPageLoader = loader;
      loader.start();
      return loader;
    }
    if (this.doc.readyState === 'loading' || !this.doc.querySelector('[data-dbpantry-splash]')) {
      var started = null;
      function run() {
        if (started) { return; }
        started = start();
      }
      this.doc.addEventListener('DOMContentLoaded', run, { once: true });
      this.win.setTimeout(run, 0);
      return started;
    }
    return start();
  };
  PageLoaderBootstrap.auto = function () {
    return new PageLoaderBootstrap().boot();
  };

  global.dbpantryPageLoaderKit = {
    PageLoaderConfig: PageLoaderConfig,
    PageLoaderEventBus: PageLoaderEventBus,
    DomAttributeBag: DomAttributeBag,
    ReadinessProbe: ReadinessProbe,
    SplashScreen: SplashScreen,
    PageLoader: PageLoader,
    PageLoaderBootstrap: PageLoaderBootstrap
  };

  PageLoaderBootstrap.auto();
})(typeof window !== 'undefined' ? window : this);
