// Command palette: ⌘K / Ctrl+K or / search over pages and actions; g-then-letter jumps.
(function () {
  'use strict';

  var modalEl = document.getElementById('mdb-cmd');
  var qEl = document.getElementById('mdb-cmd-q');
  var listEl = document.getElementById('mdb-cmd-list');
  if (!modalEl || !qEl || !listEl) { return; }

  var tabler = window.tabler || {};
  var actionsCache = null;
  var results = [];
  var active = 0;
  var gPending = null;

  var PAGES = [
    { kind: 'page', title: 'Overview', hint: 'Health, funnel and what needs attention', href: '/', keys: 'go' },
    { kind: 'page', title: 'Services', hint: 'Containers: start, stop, restart, logs', href: '/services', keys: 'gs' },
    { kind: 'page', title: 'Images', hint: 'Locally built engine images', href: '/images', keys: 'gi' },
    { kind: 'page', title: 'Engines', hint: 'Addresses, poolers and principals', href: '/engines', keys: 'ge' },
    { kind: 'page', title: 'Databases & users', hint: 'Shared-hosting wizard over projects', href: '/databases', keys: 'gd' },
    { kind: 'page', title: 'Projects', hint: 'Password-file projects and orphans', href: '/projects', keys: 'gp' },
    { kind: 'page', title: 'Backups', hint: 'Dumps, verify and restore', href: '/backups', keys: 'gb' },
    { kind: 'page', title: 'Add-ons', hint: 'Poolers, extensions, embeds and UIs', href: '/addons', keys: 'ga' },
    { kind: 'page', title: 'Environments', hint: 'Named homes by type; copy/migrate databases', href: '/envs', keys: 'gm' },
    { kind: 'page', title: 'System', hint: 'Host and runtime facts (doctor)', href: '/system', keys: 'gy' },
    { kind: 'page', title: 'Status', hint: 'Live legend, health and jobs', href: '/status', keys: 'gt' },
    { kind: 'page', title: 'How to connect', hint: 'Publish modes and engine addresses', href: '/connect', keys: 'gc' },
    { kind: 'page', title: 'Configuration', hint: 'MDB_ keys and instance summary', href: '/config', keys: 'gf' },
    { kind: 'page', title: 'Operations', hint: 'Security, topology, health, tokens', href: '/operations', keys: 'gn' },
    { kind: 'page', title: 'Jobs', hint: 'In-memory jobs and audit log', href: '/jobs', keys: 'gj' },
    { kind: 'page', title: 'Help', hint: 'How to operate this dashboard', href: '/help', keys: 'gh' },
    { kind: 'page', title: 'About', hint: 'Version, licences and build identity', href: '/about', keys: 'gv' }
  ];

  function modal() {
    return tabler.Modal ? tabler.Modal.getOrCreateInstance(modalEl) : null;
  }

  function isOpen() {
    return modalEl.classList.contains('show');
  }

  var isMac = /mac|iphone|ipad/i.test((navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '');
  document.querySelectorAll('[data-cmd-shortcut]').forEach(function (k) { k.textContent = isMac ? '⌘K' : 'Ctrl K'; });

  // A failed fetch is not cached, so the next open tries again.
  function loadActions() {
    if (actionsCache) { return Promise.resolve(actionsCache); }
    return fetch('/api/v1/actions', { credentials: 'same-origin', cache: 'no-store' })
      .then(function (r) { if (!r.ok) { throw new Error(String(r.status)); } return r.json(); })
      .then(function (rows) {
        actionsCache = (rows || []).map(function (a) {
          return {
            kind: 'action',
            title: a.title || a.name,
            hint: a.command || a.name,
            name: a.name,
            allowed: !!a.allowed,
            keys: ''
          };
        });
        return actionsCache;
      })
      .catch(function () { return []; });
  }

  function match(item, q) {
    if (!q) { return true; }
    var hay = (item.title + ' ' + (item.hint || '') + ' ' + (item.name || '') + ' ' + (item.keys || '')).toLowerCase();
    return q.split(/\s+/).every(function (part) { return part && hay.indexOf(part) >= 0; });
  }

  function render() {
    var q = (qEl.value || '').trim().toLowerCase();
    var pages = PAGES.filter(function (p) { return match(p, q); });
    var acts = (actionsCache || []).filter(function (a) { return match(a, q); });
    results = pages.concat(acts).slice(0, 40);
    if (active >= results.length) { active = Math.max(0, results.length - 1); }
    listEl.textContent = '';
    if (!results.length) {
      var empty = document.createElement('div');
      empty.className = 'text-secondary text-center py-4';
      empty.textContent = 'No matches';
      listEl.appendChild(empty);
      return;
    }
    var lastKind = '';
    results.forEach(function (item, i) {
      if (item.kind !== lastKind) {
        lastKind = item.kind;
        var head = document.createElement('div');
        head.className = 'mdb-cmd-section';
        head.textContent = item.kind === 'page' ? 'Pages' : 'Actions';
        listEl.appendChild(head);
      }
      var row = document.createElement('button');
      row.type = 'button';
      row.className = 'mdb-cmd-row' + (i === active ? ' is-active' : '') + (item.kind === 'action' && !item.allowed ? ' is-disabled' : '');
      row.setAttribute('role', 'option');
      row.setAttribute('aria-selected', i === active ? 'true' : 'false');
      row.dataset.index = String(i);
      var main = document.createElement('div');
      main.className = 'mdb-cmd-row-main';
      var title = document.createElement('div');
      title.className = 'mdb-cmd-title';
      title.textContent = item.title;
      if (item.kind === 'action' && !item.allowed) {
        var tag = document.createElement('span');
        tag.className = 'badge bg-secondary-lt ms-2';
        tag.textContent = 'unavailable';
        title.appendChild(tag);
      }
      var hint = document.createElement('div');
      hint.className = 'mdb-cmd-hint text-secondary';
      hint.textContent = item.hint || '';
      main.appendChild(title);
      main.appendChild(hint);
      row.appendChild(main);
      if (item.keys) {
        var keys = document.createElement('span');
        keys.className = 'mdb-cmd-keys';
        item.keys.split('').forEach(function (ch, idx) {
          if (idx) {
            var sep = document.createElement('span');
            sep.className = 'text-secondary mx-1';
            sep.textContent = 'then';
            keys.appendChild(sep);
          }
          var k = document.createElement('kbd');
          k.className = 'mdb-cmd-kbd';
          k.textContent = ch;
          keys.appendChild(k);
        });
        row.appendChild(keys);
      }
      row.addEventListener('mouseenter', function () { active = i; highlight(); });
      row.addEventListener('click', function () { run(item); });
      listEl.appendChild(row);
    });
  }

  function highlight() {
    listEl.querySelectorAll('.mdb-cmd-row').forEach(function (row, i) {
      row.classList.toggle('is-active', i === active);
      row.setAttribute('aria-selected', i === active ? 'true' : 'false');
    });
    var cur = listEl.querySelector('.mdb-cmd-row.is-active');
    if (cur && cur.scrollIntoView) { cur.scrollIntoView({ block: 'nearest' }); }
  }

  function run(item) {
    if (!item) { return; }
    if (item.kind === 'page') {
      close();
      window.location.href = item.href;
      return;
    }
    if (item.kind === 'action') {
      if (!item.allowed) {
        if (window.mdbToast) { window.mdbToast('Not available: ' + item.title, 'danger'); }
        return;
      }
      close();
      if (typeof window.mdbRunAction === 'function') {
        window.mdbRunAction(item.name, {});
      }
    }
  }

  // Pages show at once; actions join the list when they arrive.
  function open() {
    qEl.value = '';
    active = 0;
    render();
    var m = modal();
    if (m) { m.show(); }
    loadActions().then(function () { if (isOpen()) { render(); } });
  }

  modalEl.addEventListener('shown.bs.modal', function () { qEl.focus(); });

  function close() {
    var m = modal();
    if (m) { m.hide(); }
    if (gPending) { clearTimeout(gPending.timer); gPending = null; }
  }

  function goChord(second) {
    var keys = 'g' + second;
    var page = PAGES.filter(function (p) { return p.keys === keys; })[0];
    if (page) { window.location.href = page.href; }
  }

  document.querySelectorAll('[data-cmd-open]').forEach(function (b) {
    b.addEventListener('click', function (e) {
      e.preventDefault();
      open();
    });
  });

  qEl.addEventListener('input', function () {
    active = 0;
    render();
  });

  qEl.addEventListener('keydown', function (e) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (results.length) { active = (active + 1) % results.length; highlight(); }
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (results.length) { active = (active - 1 + results.length) % results.length; highlight(); }
    } else if (e.key === 'Enter') {
      e.preventDefault();
      run(results[active]);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  });

  document.addEventListener('keydown', function (e) {
    var tag = (e.target && e.target.tagName) || '';
    var typing = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (e.target && e.target.isContentEditable);
    if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
      e.preventDefault();
      if (isOpen()) { close(); } else { open(); }
      return;
    }
    if (isOpen() && e.key === 'Escape') {
      e.preventDefault();
      close();
      return;
    }
    if (typing || e.metaKey || e.ctrlKey || e.altKey) { return; }
    if (e.key === '/' && !isOpen() && !document.querySelector('.modal.show, .offcanvas.show')) {
      e.preventDefault();
      open();
      return;
    }
    if (gPending) {
      var second = (e.key || '').toLowerCase();
      clearTimeout(gPending.timer);
      gPending = null;
      if (/^[a-z]$/.test(second)) {
        e.preventDefault();
        goChord(second);
      }
      return;
    }
    if ((e.key === 'g' || e.key === 'G') && !isOpen()) {
      gPending = {
        timer: setTimeout(function () { gPending = null; }, 900)
      };
    }
  });

  window.mdbOpenCommandPalette = open;
})();
