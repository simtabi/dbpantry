// Add-ons: Status/Engine toggles + wizard (engine → catalog → review → job).
// Mutations go only through window.mdbRunAction → POST /api/v1/jobs.
(function () {
  'use strict';

  var QUEUE_KEY = 'mdb-addon-queue';

  function runAddon(op, id, engine) {
    if (typeof window.mdbRunAction !== 'function') {
      if (window.mdbToast) { window.mdbToast('Actions are not available on this page', 'danger'); }
      return;
    }
    var action = op === 'enable' ? 'addon.enable' : 'addon.disable';
    window.mdbRunAction(action, { id: id, engine: engine || '' });
  }

  document.addEventListener('click', function (e) {
    var sw = e.target.closest('input[data-addon-toggle]');
    if (!sw || sw.disabled) { return; }
    e.preventDefault();
    e.stopPropagation();
    var wantOn = !sw.checked;
    runAddon(wantOn ? 'enable' : 'disable', sw.getAttribute('data-id'), sw.getAttribute('data-engine'));
  }, true);

  // Reassigned by mount() after Live/forced partial refresh replaces #content.
  var root = null;

  function i18n(name, fallback) {
    if (!root) { return fallback || ''; }
    return root.getAttribute('data-i18n-' + name) || fallback || '';
  }

  function rebindTips(el) {
    if (typeof window.mdbBindHints === 'function') {
      window.mdbBindHints(el || root);
    }
  }

  function setTip(el, tip) {
    if (!el) { return; }
    if (tip) {
      el.setAttribute('title', tip);
      el.setAttribute('data-bs-original-title', tip);
      if (!el.getAttribute('data-bs-toggle') || el.getAttribute('data-bs-toggle') === 'tooltip') {
        el.setAttribute('data-bs-toggle', 'tooltip');
      }
    } else {
      el.removeAttribute('title');
      el.removeAttribute('data-bs-original-title');
    }
  }

  function setControlEnabled(el, on, tipWhenOff) {
    if (!el) { return; }
    var enable = !!on;
    if (el.tagName === 'A') {
      el.classList.toggle('disabled', !enable);
      el.setAttribute('aria-disabled', enable ? 'false' : 'true');
      el.setAttribute('tabindex', enable ? '0' : '-1');
    } else {
      el.disabled = !enable;
      el.setAttribute('aria-disabled', enable ? 'false' : 'true');
      el.classList.toggle('disabled', !enable);
    }
    setTip(el, enable ? (el.getAttribute('data-addon-tip-on') || '') : (tipWhenOff || ''));
  }

  function loadQueue() {
    try {
      var raw = sessionStorage.getItem(QUEUE_KEY);
      var parsed = raw ? JSON.parse(raw) : [];
      return Array.isArray(parsed) ? parsed : [];
    } catch (err) {
      return [];
    }
  }

  function saveQueue() {
    try {
      sessionStorage.setItem(QUEUE_KEY, JSON.stringify(queue));
    } catch (err) { /* private mode */ }
  }

  var queue = loadQueue();
  var mode = 'multi';
  var scope = 'all';
  var selected = [];
  var kind = '';
  var maxReached = 1;
  var currentStep = 1;
  var wizardBound = false;

  function parseEngines(raw) {
    return String(raw || '').split(',').map(function (s) { return s.trim(); }).filter(Boolean);
  }

  function canGoCatalog() {
    if (scope === 'all') { return true; }
    if (mode === 'single') { return selected.length === 1; }
    return selected.length >= 1;
  }

  function catalogNeedTip() {
    if (canGoCatalog()) { return ''; }
    return mode === 'single'
      ? i18n('need-engine', 'Select one engine, or choose All engines')
      : i18n('need-engines', 'Select at least one engine, or choose All engines');
  }

  function panels() {
    return root ? root.querySelectorAll('[data-addon-panel]') : [];
  }

  function setStep(n, force) {
    if (!root) { return; }
    n = Number(n);
    if (!force) {
      if (n >= 2 && !canGoCatalog() && maxReached < 2) { return; }
      if (n >= 3 && maxReached < 2 && !canGoCatalog()) { return; }
    }
    if (n >= 2 && !canGoCatalog() && currentStep < 2) { return; }
    currentStep = n;
    if (n > maxReached) { maxReached = n; }
    panels().forEach(function (p) {
      var on = String(p.getAttribute('data-addon-panel')) === String(n);
      p.classList.toggle('d-none', !on);
    });
    root.querySelectorAll('#mdb-addon-steps .step-item').forEach(function (s) {
      var label = s.getAttribute('data-step-label');
      s.classList.toggle('active', label === String(n));
    });
    if (n === 2) { filterRows(); }
    if (n === 3) { renderReview(); }
    syncURL();
    syncGates();
  }

  function syncURL() {
    if (!(window.history && window.history.replaceState)) { return; }
    var url = new URL(window.location.href);
    url.searchParams.delete('engine');
    url.searchParams.delete('engines');
    if (scope === 'all' || !selected.length) {
      // no filter params
    } else if (mode === 'single' && selected.length === 1) {
      url.searchParams.set('engine', selected[0]);
    } else if (selected.length === 1) {
      url.searchParams.set('engines', selected[0]);
    } else if (selected.length > 1) {
      url.searchParams.set('engines', selected.join(','));
    }
    window.history.replaceState({}, '', url.pathname + url.search);
  }

  function selectionLabel() {
    if (scope === 'all' || !selected.length) {
      return i18n('showing-all', 'Showing all engines');
    }
    if (selected.length === 1) {
      return i18n('selected-one', '1 engine selected (%s)').replace('%s', selected[0]);
    }
    return i18n('selected-many', '%d engines selected').replace('%d', String(selected.length));
  }

  function offSelected() {
    var off = [];
    selected.forEach(function (eng) {
      var hit = root.querySelector('[data-addon-engine-pick="1"][data-addon-engine="' + eng + '"]');
      if (hit && hit.getAttribute('data-addon-enabled') !== 'true') {
        off.push(eng);
      }
    });
    return off;
  }

  function syncPickerUI() {
    if (!root) { return; }
    root.setAttribute('data-mode', mode);
    root.setAttribute('data-scope', scope);
    root.setAttribute('data-engines', selected.join(','));
    root.setAttribute('data-engine', selected.length === 1 ? selected[0] : '');
    root.querySelectorAll('[data-addon-mode]').forEach(function (b) {
      var on = b.getAttribute('data-addon-mode') === mode;
      b.classList.toggle('btn-primary', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
    var allBtn = root.querySelector('[data-addon-all]');
    if (allBtn) {
      var allOn = scope === 'all';
      allBtn.classList.toggle('btn-azure', allOn);
      allBtn.classList.toggle('btn-outline-azure', !allOn);
      allBtn.setAttribute('aria-pressed', allOn ? 'true' : 'false');
    }
    root.querySelectorAll('[data-addon-engine-pick="1"]:not([data-addon-all])').forEach(function (card) {
      var eng = card.getAttribute('data-addon-engine') || '';
      var on = scope === 'engines' && selected.indexOf(eng) >= 0;
      var wrap = card.closest('.mdb-addon-engine-card');
      if (wrap) { wrap.classList.toggle('mdb-addon-engine-card-selected', on); }
      card.classList.toggle('mdb-addon-engine-card-selected', on);
      card.setAttribute('aria-selected', on ? 'true' : 'false');
      var check = (wrap || card).querySelector('.mdb-addon-engine-check');
      if (check) {
        check.classList.toggle('is-on', on);
        check.classList.toggle('is-multi', mode === 'multi');
      }
    });
    var list = root.querySelector('#mdb-addon-engines');
    if (list) { list.setAttribute('aria-multiselectable', mode === 'multi' ? 'true' : 'false'); }
    var summary = root.querySelector('[data-addon-selection-summary]');
    if (summary) { summary.textContent = selectionLabel(); }
    var catalogFilter = root.querySelector('[data-addon-catalog-filter]');
    if (catalogFilter) { catalogFilter.textContent = selectionLabel(); }

    var banner = root.querySelector('[data-addon-off-banner]');
    var off = offSelected();
    if (banner) {
      banner.classList.toggle('d-none', off.length === 0);
      var txt = banner.querySelector('[data-addon-off-banner-text]');
      if (txt && off.length) {
        txt.textContent = i18n('off-banner', 'Selected engines that are off need their profile enabled before add-ons can run: %s')
          .replace('%s', off.join(', '));
      }
    }
  }

  function syncGates() {
    if (!root) { return; }
    var nextCat = root.querySelector('[data-addon-gate="catalog"]');
    setControlEnabled(nextCat, canGoCatalog(), catalogNeedTip());
    if (nextCat && canGoCatalog()) {
      setTip(nextCat, nextCat.getAttribute('data-addon-tip-on') || i18n('showing-all', ''));
    }

    root.querySelectorAll('[data-addon-step-link]').forEach(function (a) {
      var n = Number(a.getAttribute('data-addon-step-link'));
      var ok = n === 1 || n <= maxReached || (n === 2 && canGoCatalog()) || (n === 3 && maxReached >= 2);
      setControlEnabled(a, ok, i18n('need-step', 'Finish the earlier step first'));
    });

    var apply = document.getElementById('mdb-addon-apply');
    setControlEnabled(apply, queue.length > 0, i18n('need-queue', 'Queue a change in the catalog first'));
    rebindTips(root);
  }

  function filterRows() {
    if (!root) { return; }
    var visibleCount = 0;
    var filter = scope === 'all' ? [] : selected;
    root.querySelectorAll('[data-addon-engine-block]').forEach(function (block) {
      var eng = block.getAttribute('data-addon-engine-block');
      var okEng = !filter.length || filter.indexOf(eng) >= 0;
      block.classList.toggle('d-none', !okEng);
    });
    root.querySelectorAll('[data-addon-row]').forEach(function (tr) {
      var okEng = !filter.length || filter.indexOf(tr.getAttribute('data-engine')) >= 0;
      var okKind = !kind || tr.getAttribute('data-kind') === kind;
      var show = okEng && okKind;
      tr.classList.toggle('d-none', !show);
      if (show) { visibleCount += 1; }
    });
    root.querySelectorAll('[data-addon-kind-block]').forEach(function (block) {
      if (block.closest('.mdb-addon-engine.d-none')) {
        block.classList.add('d-none');
        return;
      }
      var visible = false;
      block.querySelectorAll('[data-addon-row]').forEach(function (tr) {
        if (!tr.classList.contains('d-none')) { visible = true; }
      });
      block.classList.toggle('d-none', !visible);
    });
    root.querySelectorAll('[data-addon-engine-block]').forEach(function (block) {
      if (block.classList.contains('d-none')) { return; }
      var any = false;
      block.querySelectorAll('[data-addon-row]').forEach(function (tr) {
        if (!tr.classList.contains('d-none')) { any = true; }
      });
      if (!any && kind) { block.classList.add('d-none'); }
    });
    var empty = root.querySelector('[data-addon-catalog-empty]');
    var manage = root.querySelector('.mdb-addon-manage');
    if (empty) { empty.classList.toggle('d-none', visibleCount > 0); }
    if (manage) { manage.classList.toggle('d-none', visibleCount === 0); }
  }

  function setMode(next) {
    mode = next === 'single' ? 'single' : 'multi';
    if (mode === 'single' && selected.length > 1) {
      selected = [selected[0]];
      scope = 'engines';
    }
    syncPickerUI();
    filterRows();
    syncURL();
    syncGates();
  }

  function clearSelection() {
    selected = [];
    scope = 'all';
    syncPickerUI();
    filterRows();
    syncURL();
    syncGates();
  }

  function pickEngine(eng) {
    eng = eng || '';
    if (!eng) {
      clearSelection();
      return;
    }
    scope = 'engines';
    if (mode === 'single') {
      selected = selected.length === 1 && selected[0] === eng ? [] : [eng];
      if (!selected.length) { scope = 'all'; }
    } else {
      var i = selected.indexOf(eng);
      if (i >= 0) { selected.splice(i, 1); }
      else { selected.push(eng); }
      if (!selected.length) { scope = 'all'; }
    }
    syncPickerUI();
    filterRows();
    syncURL();
    syncGates();
  }

  function setKind(k) {
    kind = k || '';
    root.querySelectorAll('[data-addon-kind]').forEach(function (b) {
      b.classList.toggle('btn-primary', (b.getAttribute('data-addon-kind') || '') === kind);
    });
    filterRows();
  }

  function queueKey(item) {
    return item.op + ':' + item.id + ':' + item.engine;
  }

  function addQueue(item) {
    var key = queueKey(item);
    queue = queue.filter(function (x) { return queueKey(x) !== key; });
    queue.push(item);
    saveQueue();
    if (window.mdbToast) {
      window.mdbToast(i18n('queued', 'Queued') + ': ' + item.op + ' ' + item.id, 'success');
    }
    renderReview();
    syncGates();
  }

  function renderReview() {
    var list = document.getElementById('mdb-addon-review');
    if (!list) { return; }
    var empty = list.querySelector('[data-addon-review-empty]');
    var apply = document.getElementById('mdb-addon-apply');
    var note = document.getElementById('mdb-addon-preload-note');
    list.querySelectorAll('[data-addon-review-item]').forEach(function (n) { n.remove(); });
    var preload = false;
    queue.forEach(function (item) {
      var li = document.createElement('li');
      li.className = 'list-group-item d-flex align-items-start gap-2';
      li.setAttribute('data-addon-review-item', '1');
      var cmd = 'dbpantry addon ' + item.op + ' ' + item.id + ' --engine ' + item.engine;
      var body = document.createElement('div');
      body.className = 'flex-fill min-w-0';
      var title = document.createElement('div');
      title.className = 'fw-medium';
      var verb = item.op === 'enable' ? i18n('enable', 'Enable') : i18n('disable', 'Disable');
      title.textContent = verb + ' ' + (item.title || item.id);
      var mono = document.createElement('div');
      mono.className = 'small mdb-mono text-secondary text-truncate';
      mono.textContent = cmd;
      body.appendChild(title);
      body.appendChild(mono);
      if (item.preload && item.op === 'enable') {
        preload = true;
        var tip = document.createElement('div');
        tip.className = 'small text-yellow';
        tip.textContent = i18n('preload-tip', 'shared_preload_libraries change — restart pg after apply');
        body.appendChild(tip);
      }
      var rm = document.createElement('button');
      rm.type = 'button';
      rm.className = 'btn btn-sm';
      rm.textContent = i18n('remove', 'Remove');
      rm.addEventListener('click', function () {
        queue = queue.filter(function (x) { return queueKey(x) !== queueKey(item); });
        saveQueue();
        renderReview();
        syncGates();
      });
      li.appendChild(body);
      li.appendChild(rm);
      list.appendChild(li);
    });
    if (empty) { empty.classList.toggle('d-none', queue.length > 0); }
    if (apply) {
      setControlEnabled(apply, queue.length > 0, i18n('need-queue', 'Queue a change in the catalog first'));
    }
    if (note) { note.classList.toggle('d-none', !preload); }
  }

  function applyQueue() {
    if (!queue.length) { return; }
    var item = queue.shift();
    saveQueue();
    renderReview();
    syncGates();
    runAddon(item.op, item.id, item.engine);
  }

  // Document-level so remount after partial refresh does not need a new bind.
  if (!wizardBound) {
    wizardBound = true;
    document.addEventListener('click', function (e) {
      if (!e.target.closest('#mdb-addon-wizard')) { return; }
      var modeBtn = e.target.closest('[data-addon-mode]');
      if (modeBtn) {
        e.preventDefault();
        setMode(modeBtn.getAttribute('data-addon-mode') || 'multi');
        return;
      }
      var eng = e.target.closest('[data-addon-engine-pick]');
      if (eng) {
        e.preventDefault();
        pickEngine(eng.getAttribute('data-addon-engine') || '');
        return;
      }
      var k = e.target.closest('[data-addon-kind]');
      if (k && k.tagName === 'BUTTON') {
        e.preventDefault();
        setKind(k.getAttribute('data-addon-kind') || '');
        return;
      }
      var next = e.target.closest('[data-addon-next]');
      if (next) {
        e.preventDefault();
        if (next.disabled || next.getAttribute('aria-disabled') === 'true') { return; }
        setStep(next.getAttribute('data-addon-next'));
        return;
      }
      var stepLink = e.target.closest('[data-addon-step-link]');
      if (stepLink) {
        e.preventDefault();
        if (stepLink.getAttribute('aria-disabled') === 'true' || stepLink.classList.contains('disabled')) {
          return;
        }
        setStep(stepLink.getAttribute('data-addon-step-link'));
        return;
      }
      var q = e.target.closest('[data-addon-queue]');
      if (q) {
        e.preventDefault();
        if (q.disabled) { return; }
        addQueue({
          op: q.getAttribute('data-addon-queue'),
          id: q.getAttribute('data-id'),
          engine: q.getAttribute('data-engine'),
          title: q.getAttribute('data-title'),
          preload: q.getAttribute('data-preload') === 'true'
        });
        return;
      }
      if (e.target.closest('#mdb-addon-apply')) {
        e.preventDefault();
        var applyBtn = document.getElementById('mdb-addon-apply');
        if (applyBtn && (applyBtn.disabled || applyBtn.getAttribute('aria-disabled') === 'true')) { return; }
        applyQueue();
      }
    });
  }

  function mount() {
    root = document.getElementById('mdb-addon-wizard');
    if (!root) { return; }
    mode = root.getAttribute('data-mode') === 'single' ? 'single' : 'multi';
    scope = root.getAttribute('data-scope') === 'engines' ? 'engines' : 'all';
    selected = parseEngines(root.getAttribute('data-engines') || root.getAttribute('data-engine') || '');
    kind = '';
    maxReached = 1;
    currentStep = 1;
    queue = loadQueue();
    try {
      var params = new URLSearchParams(window.location.search);
      var multi = params.get('engines');
      var one = params.get('engine');
      if (multi) {
        mode = 'multi';
        selected = parseEngines(multi);
        scope = selected.length ? 'engines' : 'all';
      } else if (one) {
        mode = 'single';
        selected = [one];
        scope = 'engines';
      }
    } catch (err) { /* ignore */ }
    if (selected.length) { maxReached = 2; }
    syncPickerUI();
    filterRows();
    if (selected.length) { setStep(2, true); }
    else { setStep(1, true); }
    if (queue.length) { renderReview(); }
    syncGates();
  }

  window.mdbAddonMount = mount;
  mount();
})();
