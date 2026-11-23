// The dashboard's behaviour: refresh the page's content every five seconds
// from the server (one template renders it, so there is no second renderer
// here), switch the colour scheme without a reload, reveal a blurred
// password, and copy a block. A page marked data-no-refresh (a project's
// page, which holds a password) is never refreshed. The Live badge pauses
// and resumes that refresh; Back uses history when it can.
(function () {
  var target = document.querySelector('[data-refresh-target]');
  var badge = document.querySelector('.mdb-refresh');
  var label = document.querySelector('[data-refresh-label]');
  var failures = 0;
  var tabler = window.tabler || {};
  var refreshPaused = false;
  try {
    refreshPaused = localStorage.getItem('dbpantry-refresh') === 'paused';
  } catch (err) { /* storage blocked */ }

  // In-page back/forward: a session stack of path+search, so Forward works
  // after Back and buttons disable when there is nowhere to go (browser
  // history.length is not useful for Forward).
  var HIST_KEY = 'dbpantry-hist';
  var HIST_NAV = 'dbpantry-hist-nav';
  function histState() {
    try {
      var raw = sessionStorage.getItem(HIST_KEY);
      if (!raw) { return { paths: [], i: -1 }; }
      var s = JSON.parse(raw);
      if (!Array.isArray(s.paths) || typeof s.i !== 'number') { return { paths: [], i: -1 }; }
      return s;
    } catch (err) { return { paths: [], i: -1 }; }
  }
  function histSave(s) {
    try { sessionStorage.setItem(HIST_KEY, JSON.stringify(s)); } catch (err) { /* blocked */ }
  }
  function histHere() {
    return location.pathname + location.search + location.hash;
  }
  function histUpdateButtons() {
    var s = histState();
    document.querySelectorAll('[data-nav-back]').forEach(function (el) {
      el.disabled = s.i <= 0;
    });
    document.querySelectorAll('[data-nav-forward]').forEach(function (el) {
      el.disabled = s.i < 0 || s.i >= s.paths.length - 1;
    });
  }
  function histRecord() {
    var here = histHere();
    var s = histState();
    var viaBtn = false;
    try { viaBtn = sessionStorage.getItem(HIST_NAV) === '1'; sessionStorage.removeItem(HIST_NAV); } catch (err) { /* blocked */ }
    if (viaBtn) {
      histUpdateButtons();
      return;
    }
    if (s.i >= 0 && s.paths[s.i] === here) {
      histUpdateButtons();
      return;
    }
    s.paths = s.paths.slice(0, s.i + 1);
    s.paths.push(here);
    if (s.paths.length > 50) {
      s.paths = s.paths.slice(s.paths.length - 50);
    }
    s.i = s.paths.length - 1;
    histSave(s);
    histUpdateButtons();
  }
  function histGo(delta, fallback) {
    var s = histState();
    var next = s.i + delta;
    if (next < 0 || next >= s.paths.length) {
      if (delta < 0 && fallback) { location.href = fallback; }
      return;
    }
    s.i = next;
    histSave(s);
    try { sessionStorage.setItem(HIST_NAV, '1'); } catch (err) { /* blocked */ }
    location.href = s.paths[next];
  }
  histRecord();

  function setState(state, text, tip) {
    if (!badge) { return; }
    badge.setAttribute('data-state', state);
    if (label) { label.textContent = text; }
    var dot = badge.querySelector('[data-refresh-dot]');
    if (dot) {
      var color = state === 'error' ? 'red' : (state === 'paused' || state === 'stale' ? 'yellow' : 'green');
      dot.className = 'status-dot me-1 status-' + color + (state === 'live' ? ' status-dot-animated' : '');
    }
    badge.setAttribute('aria-pressed', state === 'live' ? 'true' : 'false');
    if (tip) { badge.title = tip; }
  }

  function applyPauseUI() {
    if (!badge || badge.disabled) { return; }
    if (refreshPaused) {
      setState('paused', 'Paused', 'Paused: click to resume the five-second refresh');
    } else if (failures > 2) {
      setState('error', 'Disconnected', 'Disconnected: three failed refreshes — click to retry');
    } else if (failures > 0) {
      setState('stale', 'Retrying', 'Retrying: last refresh failed — click to pause');
    } else {
      setState('live', 'Live', 'Live: auto-refresh every 5s — click to pause');
    }
  }

  // disposeHints tears down Tabler tip/popover instances before a partial
  // refresh replaces their hosts; without it, tip nodes stay in <body> and
  // stack on top of each other (orphaned "Stream container logs" bubbles).
  function disposeHints(root) {
    root = root || document;
    if (tabler.Tooltip) {
      root.querySelectorAll('[data-bs-toggle="tooltip"], [data-bs-original-title], a[title], button[title], [title]').forEach(function (el) {
        var tip = tabler.Tooltip.getInstance(el);
        if (tip) { tip.dispose(); }
      });
    }
    if (tabler.Popover) {
      root.querySelectorAll('[data-bs-toggle="popover"]').forEach(function (el) {
        var pop = tabler.Popover.getInstance(el);
        if (pop) { pop.dispose(); }
      });
    }
  }

  function clearOrphanHints() {
    document.querySelectorAll('body > .tooltip, body > .popover').forEach(function (n) { n.remove(); });
  }

  function hideHints() {
    if (tabler.Tooltip) {
      document.querySelectorAll('[data-bs-original-title], [data-bs-toggle="tooltip"]').forEach(function (el) {
        var tip = tabler.Tooltip.getInstance(el);
        if (tip) { tip.hide(); }
      });
    }
    if (tabler.Popover) {
      document.querySelectorAll('[data-bs-toggle="popover"]').forEach(function (el) {
        var pop = tabler.Popover.getInstance(el);
        if (pop) { pop.hide(); }
      });
    }
    clearOrphanHints();
  }

  // bindHints wires tooltips/popovers on interactive controls only. Items
  // inside an open dropdown keep native title (or none): promoting those to
  // Bootstrap tips left bubbles floating over the table while the menu was
  // clipped. Delay avoids the dense Quick-access row flashing two tips.
  function bindHints(root) {
    root = root || document;
    var skipToggle = { dropdown: 1, collapse: 1, offcanvas: 1, modal: 1, tab: 1 };
    if (tabler.Tooltip) {
      root.querySelectorAll('a[title], button[title], [data-bs-toggle="tooltip"]').forEach(function (el) {
        if (el.closest('.dropdown-menu')) { return; }
        var toggle = el.getAttribute('data-bs-toggle') || '';
        if (skipToggle[toggle] || toggle === 'popover') { return; }
        var existing = tabler.Tooltip.getInstance(el);
        if (existing) { existing.dispose(); }
        tabler.Tooltip.getOrCreateInstance(el, {
          container: 'body',
          trigger: 'hover focus',
          placement: el.getAttribute('data-bs-placement') || 'auto',
          delay: { show: 400, hide: 80 },
          animation: false
        });
      });
    }
    if (tabler.Popover) {
      root.querySelectorAll('[data-bs-toggle="popover"]').forEach(function (el) {
        var existing = tabler.Popover.getInstance(el);
        if (existing) { existing.dispose(); }
        tabler.Popover.getOrCreateInstance(el, {
          container: 'body',
          trigger: el.getAttribute('data-bs-trigger') || 'hover focus',
          placement: el.getAttribute('data-bs-placement') || 'auto',
          delay: { show: 400, hide: 80 },
          html: false
        });
      });
    }
  }

  // bindMenus gives in-page (and nav dropend) dropdowns Popper strategy:fixed
  // so .table-responsive / .card overflow no longer clips them.
  function bindMenus(root) {
    root = root || document;
    if (!tabler.Dropdown) { return; }
    root.querySelectorAll('[data-mdb-engine-menu] [data-bs-toggle="dropdown"], [data-mdb-fixed-dropdown]').forEach(function (el) {
      if (el.getAttribute('aria-expanded') === 'true') { return; }
      var existing = tabler.Dropdown.getInstance(el);
      if (existing) { existing.dispose(); }
      tabler.Dropdown.getOrCreateInstance(el, {
        autoClose: el.getAttribute('data-bs-auto-close') || 'outside',
        popperConfig: function (def) {
          var next = Object.assign({}, def, { strategy: 'fixed' });
          var mods = (def.modifiers || []).slice();
          var hasOffset = false;
          mods = mods.map(function (m) {
            if (m.name === 'preventOverflow') {
              return Object.assign({}, m, { options: Object.assign({}, m.options || {}, { boundary: 'viewport', altBoundary: true }) });
            }
            if (m.name === 'offset') { hasOffset = true; }
            return m;
          });
          if (!hasOffset) {
            mods.push({ name: 'offset', options: { offset: [0, 4] } });
          }
          next.modifiers = mods;
          return next;
        }
      });
    });
  }

  function closeMenus(scope) {
    if (!tabler.Dropdown) { return; }
    (scope || document).querySelectorAll('[data-bs-toggle="dropdown"]').forEach(function (el) {
      var d = tabler.Dropdown.getInstance(el);
      if (d) { d.hide(); }
    });
  }
  window.mdbCloseMenus = closeMenus;

  var openMenuCount = 0;
  function menusOpen() {
    // Skip Live refresh while a menu, slide-in or modal is open — replacing
    // #content would wipe page-local offcanvases (engine Add-ons) mid-use.
    return openMenuCount > 0
      || !!document.querySelector('.dropdown-menu.show')
      || !!document.querySelector('.offcanvas.show')
      || !!document.querySelector('.modal.show');
  }
  window.mdbMenusOpen = menusOpen;

  function bindUI(root) {
    bindHints(root);
    bindMenus(root);
  }
  window.mdbBindHints = bindUI;

  // Status JSON slide-in: Help / Instance / footer open the same drawer.
  function openStatusJSON(refreshOnly) {
    var drawer = document.getElementById('mdb-status');
    var out = document.getElementById('mdb-status-json');
    var state = drawer && drawer.querySelector('[data-status-state]');
    if (!drawer || !out) { return; }
    if (tabler.Offcanvas && !refreshOnly) {
      tabler.Offcanvas.getOrCreateInstance(drawer).show();
    }
    if (state) { state.textContent = 'Loading…'; state.className = 'badge bg-yellow-lt'; }
    fetch('/api/v1/status', { cache: 'no-store', credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) { throw new Error('HTTP ' + r.status); }
        return r.json();
      })
      .then(function (j) {
        out.textContent = JSON.stringify(j, null, 2);
        if (state) { state.textContent = 'Live'; state.className = 'badge bg-green-lt'; }
      })
      .catch(function (err) {
        out.textContent = 'Could not load /api/v1/status: ' + (err && err.message ? err.message : err);
        if (state) { state.textContent = 'Error'; state.className = 'badge bg-red-lt'; }
      });
  }
  window.mdbOpenStatusJSON = openStatusJSON;

  // refresh replaces the page's content with the server's rendering of it.
  // The partial also carries the header's live parts (data-sync), which
  // are moved into their places (data-sync-target) outside the content.
  // force refreshes a page that does not refresh itself, after a job.
  function refresh(force) {
    if (!target) { return; }
    if (!force && (refreshPaused || document.hidden || target.hasAttribute('data-no-refresh'))) { return; }
    if (!force && window.mdbJobLocked && window.mdbJobLocked()) { return; }
    // A 5s refresh that replaces the DOM while a menu is open looks like
    // "random autoclose". Skip until every dropdown is closed.
    if (!force && menusOpen()) { return; }
    if (force && target.hasAttribute('data-no-refresh')) { return; }
    // Keep ?engine= / ?engines= (and any other query): pathname-only fetch
    // reset Add-ons and similar filtered pages every tick and looked like
    // the wizard was "stuck loading".
    var partialURL = window.location.pathname + window.location.search;
    fetch(partialURL, { cache: 'no-store', headers: { 'X-Partial': '1' } })
      .then(function (r) {
        if (!r.ok) { throw new Error('HTTP ' + r.status); }
        var at = r.headers.get('X-Generated');
        var stamp = document.querySelector('[data-generated]');
        if (at && stamp) { stamp.textContent = at; }
        return r.text();
      })
      .then(function (html) {
        disposeHints(target);
        clearOrphanHints();
        target.innerHTML = html;
        // Pages with client wizard state (Add-ons) re-mount after a forced refresh.
        if (typeof window.mdbAddonMount === 'function' && target.querySelector('#mdb-addon-wizard')) {
          window.mdbAddonMount();
        }
        target.querySelectorAll('[data-sync]').forEach(function (s) {
          var dest = document.querySelector('[data-sync-target="' + s.getAttribute('data-sync') + '"]');
          if (dest) {
            disposeHints(dest);
            dest.innerHTML = s.innerHTML;
          }
          s.remove();
        });
        failures = 0;
        applyPauseUI();
        clearOrphanHints();
        bindUI(document);
      })
      .catch(function () {
        failures += 1;
        setState(failures > 2 ? 'error' : 'stale', failures > 2 ? 'Disconnected' : 'Retrying');
      });
  }

  window.mdbRefresh = refresh;
  // Temporary pause for action busy UI (manage.js): does not write localStorage.
  window.mdbSetLivePaused = function (paused) {
    refreshPaused = !!paused;
    applyPauseUI();
  };
  setInterval(function () { refresh(false); }, 5000);
  document.addEventListener('visibilitychange', function () { refresh(false); });
  applyPauseUI();
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { bindUI(document); });
  } else {
    bindUI(document);
  }
  document.addEventListener('show.bs.dropdown', function () {
    openMenuCount += 1;
    hideHints();
  });
  document.addEventListener('hidden.bs.dropdown', function () {
    openMenuCount = Math.max(0, openMenuCount - 1);
  });
  document.addEventListener('show.bs.modal', hideHints);
  document.addEventListener('show.bs.offcanvas', hideHints);

  document.addEventListener('click', function (e) {
    if (e.target.closest('a, button, [data-action], [data-copy], [data-bs-toggle]')) {
      hideHints();
    }
    var statusBtn = e.target.closest('[data-status-json]');
    if (statusBtn) {
      e.preventDefault();
      closeMenus();
      openStatusJSON(false);
      return;
    }
    if (e.target.closest('[data-status-refresh]')) {
      e.preventDefault();
      openStatusJSON(true);
      return;
    }
    // Choosing an action / copy / nav link inside a menu should dismiss it.
    if (e.target.closest('.dropdown-menu [data-action], .dropdown-menu [data-copy], .dropdown-menu a.dropdown-item[href]')) {
      closeMenus(e.target.closest('.dropdown') || document);
    }
    var back = e.target.closest('[data-nav-back]');
    if (back) {
      e.preventDefault();
      histGo(-1, back.getAttribute('data-nav-back-fallback') || '/');
      return;
    }
    var fwd = e.target.closest('[data-nav-forward]');
    if (fwd) {
      e.preventDefault();
      histGo(1, null);
      return;
    }
    var live = e.target.closest('[data-refresh-toggle]');
    if (live && !live.disabled) {
      e.preventDefault();
      var state = live.getAttribute('data-state') || '';
      // Disconnected / Retrying: click retries immediately instead of pausing.
      if ((state === 'error' || state === 'stale') && !refreshPaused) {
        failures = 0;
        applyPauseUI();
        refresh(true);
        if (window.mdbToast) { window.mdbToast('Retrying live refresh', 'success'); }
        return;
      }
      refreshPaused = !refreshPaused;
      try {
        localStorage.setItem('dbpantry-refresh', refreshPaused ? 'paused' : 'live');
      } catch (err) { /* storage blocked */ }
      applyPauseUI();
      if (!refreshPaused) { refresh(false); }
      if (window.mdbToast) {
        window.mdbToast(refreshPaused ? 'Live refresh paused' : 'Live refresh on', 'success');
      }
      return;
    }
    var theme = e.target.closest('[data-theme-set]');
    if (theme) {
      e.preventDefault();
      var next = theme.getAttribute('data-theme-set');
      document.documentElement.setAttribute('data-bs-theme', next);
      try { localStorage.setItem('dbpantry-theme', next); } catch (err) { /* storage blocked */ }
      return;
    }
    var reveal = e.target.closest('[data-reveal]');
    if (reveal) {
      var on = target.toggleAttribute('data-revealed');
      reveal.querySelector('[data-reveal-label]').textContent = on ? 'Hide password' : 'Show password';
      return;
    }
    var copy = e.target.closest('[data-copy]');
    if (copy) {
      e.preventDefault();
      e.stopPropagation();
      var from = document.querySelector(copy.getAttribute('data-copy'));
      var text = from ? from.textContent.trim() : '';
      var done = copy.querySelector('[data-copy-label]');
      var was = done ? done.textContent : '';
      var say = function (msg, kind) {
        if (done && was !== undefined) {
          done.textContent = msg;
          setTimeout(function () { done.textContent = was; }, 2000);
        }
        if (window.mdbToast) { window.mdbToast(msg, kind || 'success'); }
      };
      if (!text) { say('Nothing to copy', 'danger'); return; }
      if (!navigator.clipboard) { say('Select and copy', 'danger'); return; }
      navigator.clipboard.writeText(text).then(function () { say('Copied'); }, function () { say('Copy blocked', 'danger'); });
    }
  });
})();
