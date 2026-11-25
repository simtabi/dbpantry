// The dashboard's management: every button marked data-action runs one
// dbpantry command through the API (POST /api/v1/jobs), after a modal that
// asks for what is missing and for the confirmation the action needs; the
// job's output streams into a drawer. The page never decides what is
// allowed: the API says (GET /api/v1/actions) and checks again on every
// request. Text from the server is set with textContent, never as HTML.
(function () {
  'use strict';

  var csrfMeta = document.querySelector('meta[name="csrf-token"]');
  var csrf = csrfMeta ? csrfMeta.getAttribute('content') : '';
  var tabler = window.tabler || {};

  function el(tag, attrs, text) {
    var e = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (k) { e.setAttribute(k, attrs[k]); });
    if (text !== undefined) { e.textContent = text; }
    return e;
  }

  // api calls the management API with the session's CSRF token; a failure
  // rejects with the server's own message.
  function api(method, path, body) {
    var opts = { method: method, credentials: 'same-origin', cache: 'no-store', headers: { 'X-CSRF-Token': csrf } };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (r) {
      if (r.status === 204) { return {}; }
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) {
          var err = new Error(j.error || ('HTTP ' + r.status));
          err.status = r.status;
          err.body = j;
          throw err;
        }
        return j;
      });
    });
  }

  function refreshPage() {
    if (window.mdbRefresh) { window.mdbRefresh(true); }
  }

  // --- Page / button busy --------------------------------------------------
  // Slow actions (fixture up, stack start, …) used to sit silent after click
  // while GET /actions + /choices ran. Show a top bar, dim the page, and put
  // a spinner on the trigger until the modal or job drawer takes over.

  var busyDepth = 0;
  var busyTrigger = null;

  function setPageBusy(on, label) {
    var bar = document.getElementById('mdb-busy-bar');
    if (on) {
      busyDepth += 1;
      document.body.classList.add('mdb-page-busy');
      if (bar) {
        bar.hidden = false;
        bar.setAttribute('aria-hidden', 'false');
        var lab = bar.querySelector('[data-busy-label]');
        if (lab) { lab.textContent = label || 'Working…'; }
      }
      if (window.mdbSetLivePaused) { window.mdbSetLivePaused(true); }
      return;
    }
    busyDepth = Math.max(0, busyDepth - 1);
    if (busyDepth > 0) {
      if (bar) {
        var lab2 = bar.querySelector('[data-busy-label]');
        if (lab2 && label) { lab2.textContent = label; }
      }
      return;
    }
    document.body.classList.remove('mdb-page-busy');
    if (bar) {
      bar.hidden = true;
      bar.setAttribute('aria-hidden', 'true');
    }
    if (window.mdbSetLivePaused) { window.mdbSetLivePaused(false); }
  }

  // setBusyLabel renames the busy bar without taking another hold on it.
  function setBusyLabel(label) {
    var lab = document.querySelector('#mdb-busy-bar [data-busy-label]');
    if (lab && busyDepth > 0) { lab.textContent = label; }
  }

  function setButtonBusy(btn, on, label) {
    if (!btn) { return; }
    if (on) {
      if (btn.getAttribute('data-mdb-busy-label') == null) {
        btn.setAttribute('data-mdb-busy-label', btn.textContent || '');
      }
      btn.classList.add('mdb-btn-busy');
      btn.setAttribute('aria-busy', 'true');
      btn.disabled = true;
      var text = label || 'Working…';
      btn.textContent = '';
      btn.appendChild(el('span', { 'class': 'mdb-btn-spin', 'aria-hidden': 'true' }));
      btn.appendChild(document.createTextNode(text));
      return;
    }
    btn.classList.remove('mdb-btn-busy');
    btn.removeAttribute('aria-busy');
    btn.disabled = false;
    var prev = btn.getAttribute('data-mdb-busy-label');
    if (prev != null) {
      btn.textContent = prev;
      btn.removeAttribute('data-mdb-busy-label');
    }
  }

  function clearTriggerBusy() {
    if (busyTrigger) {
      setButtonBusy(busyTrigger, false);
      busyTrigger = null;
    }
  }

  // --- Toasts --------------------------------------------------------------
  // Short, non-blocking feedback for copy, job start/end and API errors.
  // Tabler's Toast is used when present; otherwise the element is shown and
  // removed by a timer so the page still works without it.

  function toast(message, kind) {
    var host = document.getElementById('mdb-toasts');
    if (!host || !message) { return; }
    var tone = kind === 'danger' ? 'bg-danger text-white' : (kind === 'success' ? 'bg-success text-white' : 'bg-dark text-white');
    var node = el('div', {
      'class': 'toast show ' + tone,
      role: 'status',
      'aria-live': 'polite'
    });
    var body = el('div', { 'class': 'toast-body d-flex align-items-start gap-2' });
    body.appendChild(el('div', { 'class': 'flex-fill' }, message));
    var close = el('button', { type: 'button', 'class': 'btn-close btn-close-white', 'aria-label': 'Close' });
    close.addEventListener('click', function () { node.remove(); });
    body.appendChild(close);
    node.appendChild(body);
    host.appendChild(node);
    if (tabler.Toast) {
      var t = tabler.Toast.getOrCreateInstance(node, { delay: 4500, autohide: true });
      node.addEventListener('hidden.bs.toast', function () { node.remove(); });
      t.show();
    } else {
      setTimeout(function () { node.remove(); }, 4500);
    }
  }
  window.mdbToast = toast;

  // --- The form modal ------------------------------------------------------
  // openForm draws a modal from a description, so every action and the
  // token forms share one dialog:
  //   {title, command, fields: [{name, label, required, options, value, placeholder}],
  //    fixed: {name: value}, confirmHint, confirmValue, danger, soft, submitLabel, submit(values, confirm) -> Promise}

  var modalEl = document.getElementById('mdb-action-modal');
  var form = modalEl && modalEl.querySelector('[data-action-form]');
  var current = null;
  // modalBusy is true while a submit is in flight: Escape, backdrop and
  // Cancel must not dismiss the dialog mid-start.
  var modalBusy = false;

  function modal() { return tabler.Modal ? tabler.Modal.getOrCreateInstance(modalEl) : null; }

  function setModalLocked(locked) {
    modalBusy = !!locked;
    if (!modalEl) { return; }
    modalEl.querySelectorAll('[data-action-dismiss]').forEach(function (b) {
      b.disabled = !!locked;
      b.title = locked ? 'Wait for the action to start' : 'Close without starting a job';
    });
    modalEl.setAttribute('data-mdb-busy', locked ? '1' : '0');
  }

  function showError(msg) {
    var box = modalEl.querySelector('[data-action-error]');
    box.textContent = msg;
    box.classList.toggle('d-none', !msg);
  }

  var PROFILE_EXTRA_META = {
    ui: { title: 'Admin UIs', tip: 'Adminer, phpMyAdmin, pgAdmin and Caddy' },
    ha: { title: 'Primary & replicas', tip: 'Patroni primary (writes) + replicas (reads); rehearsal topology on one host' },
    pooler: { title: 'Poolers (all)', tip: 'Start every engine pooler profile' },
    backup: { title: 'Backup sidecars', tip: 'Scheduled dump / backup services' },
    pitr: { title: 'Point-in-time recovery', tip: 'PITR-capable backup path' },
    fixture: { title: 'Test fixture', tip: 'Disposable fixture stack for verify' }
  };

  function profileGroupOf(name, engines) {
    if ((engines || []).indexOf(name) >= 0) { return 'engines'; }
    if (name === 'pooler' || /-pooler$/.test(name)) { return 'poolers'; }
    return 'extras';
  }

  function profileTitle(name, engines) {
    var meta = PROFILE_EXTRA_META[name];
    if (meta) { return meta.title; }
    if (/-pooler$/.test(name)) {
      return name.replace(/-pooler$/, '') + ' pooler';
    }
    if ((engines || []).indexOf(name) >= 0) { return name; }
    return name;
  }

  function profileTip(name) {
    var meta = PROFILE_EXTRA_META[name];
    if (meta) { return meta.tip; }
    if (/-pooler$/.test(name)) { return 'Connection pooler for ' + name.replace(/-pooler$/, ''); }
    return 'Compose profile "' + name + '"';
  }

  function buildProfilePicker(wrap, label, id, f) {
    wrap.appendChild(label);
    var engines = f.engines || [];
    var active = {};
    (f.active || []).forEach(function (p) { if (p) { active[p] = true; } });
    var selected = {};
    var seed = String(f.value || '').split(',');
    if (!String(f.value || '').trim() && f.active && f.active.length) {
      seed = f.active.slice();
    }
    seed.forEach(function (p) {
      p = String(p || '').trim();
      if (p) { selected[p] = true; }
    });

    var picker = el('div', { 'class': 'mdb-profile-picker', id: id, 'data-multi': f.name, role: 'group', 'aria-label': f.label });
    var current = el('div', { 'class': 'mdb-profile-current' });
    var activeList = (f.active || []).slice();
    if (activeList.length) {
      current.appendChild(el('span', { 'class': 'mdb-profile-current-label' }, 'Currently configured'));
      activeList.forEach(function (p) {
        var chip = el('span', { 'class': 'badge bg-primary-lt mdb-mono' }, p);
        chip.title = profileTip(p);
        current.appendChild(chip);
      });
    } else {
      current.appendChild(el('span', { 'class': 'text-secondary small' }, 'No profiles configured yet — pick engines below, or leave empty to keep defaults.'));
    }
    picker.appendChild(current);

    var tools = el('div', { 'class': 'mdb-profile-tools' });
    function toolBtn(text, tip, fn) {
      var b = el('button', { type: 'button', 'class': 'btn btn-sm', title: tip }, text);
      b.addEventListener('click', function (e) { e.preventDefault(); fn(); });
      tools.appendChild(b);
    }
    picker.appendChild(tools);

    var groups = {
      engines: { title: 'Engines', tip: 'Database engines to include in MDB_PROFILES', items: [] },
      poolers: { title: 'Poolers', tip: 'Connection poolers for selected engines', items: [] },
      extras: { title: 'Stack extras', tip: 'UI, HA, backup and other Compose profiles', items: [] }
    };
    (f.options || []).forEach(function (o) {
      groups[profileGroupOf(o, engines)].items.push(o);
    });

    var box = el('div', { 'class': 'mdb-profile-groups' });
    if (!(f.options || []).length) {
      box.appendChild(el('div', { 'class': 'text-secondary small p-2' }, 'No profiles available'));
    }
    Object.keys(groups).forEach(function (key) {
      var g = groups[key];
      if (!g.items.length) { return; }
      var section = el('section', { 'class': 'mdb-profile-group', 'data-profile-group': key });
      var head = el('div', { 'class': 'mdb-profile-group-head' });
      head.appendChild(el('span', { 'class': 'mdb-profile-group-title' }, g.title));
      head.appendChild(el('span', { 'class': 'text-secondary small' }, g.tip));
      section.appendChild(head);
      var grid = el('div', { 'class': 'mdb-profile-grid' });
      g.items.forEach(function (o) {
        var row = el('label', {
          'class': 'mdb-profile-pick' + (active[o] ? ' is-current' : ''),
          title: profileTip(o)
        });
        var cb = el('input', { 'class': 'form-check-input', type: 'checkbox', value: o });
        if (selected[o]) { cb.checked = true; }
        var body = el('span', { 'class': 'mdb-profile-pick-body' });
        body.appendChild(el('span', { 'class': 'mdb-profile-pick-title' }, profileTitle(o, engines)));
        var meta = el('span', { 'class': 'mdb-profile-pick-meta mdb-mono' }, o);
        if (active[o]) {
          meta.appendChild(document.createTextNode(' · '));
          var on = el('span', { 'class': 'text-green' }, 'on');
          meta.appendChild(on);
        }
        body.appendChild(meta);
        row.appendChild(cb);
        row.appendChild(body);
        grid.appendChild(row);
      });
      section.appendChild(grid);
      box.appendChild(section);
    });
    picker.appendChild(box);

    var hint = el('div', { 'class': 'form-hint' }, f.hint || 'Select engines and stack extras. Empty keeps the configured MDB_PROFILES.');
    wrap.appendChild(picker);
    wrap.appendChild(hint);

    var input = el('input', { type: 'hidden', id: id + '-value', name: f.name, value: f.value || '' });
    function syncHidden() {
      var parts = [];
      picker.querySelectorAll('input[type="checkbox"]').forEach(function (cb) {
        if (cb.checked) { parts.push(cb.value); }
      });
      input.value = parts.join(',');
      picker.querySelectorAll('.mdb-profile-pick').forEach(function (row) {
        var cb = row.querySelector('input[type="checkbox"]');
        row.classList.toggle('is-checked', !!(cb && cb.checked));
      });
    }
    picker.addEventListener('change', syncHidden);

    toolBtn('Use current', 'Check the profiles already in MDB_PROFILES', function () {
      picker.querySelectorAll('input[type="checkbox"]').forEach(function (cb) {
        cb.checked = !!active[cb.value];
      });
      syncHidden();
    });
    toolBtn('Engines only', 'Select every engine profile; clear poolers and extras', function () {
      picker.querySelectorAll('input[type="checkbox"]').forEach(function (cb) {
        cb.checked = profileGroupOf(cb.value, engines) === 'engines';
      });
      syncHidden();
    });
    toolBtn('Clear', 'Uncheck all — empty keeps configured MDB_PROFILES on run', function () {
      picker.querySelectorAll('input[type="checkbox"]').forEach(function (cb) { cb.checked = false; });
      syncHidden();
    });

    syncHidden();
    wrap.appendChild(input);
    if (typeof window.mdbBindHints === 'function') { window.mdbBindHints(picker); }
    return wrap;
  }

  function field(f) {
    var id = 'mdb-f-' + f.name;
    var wrap = el('div', { 'class': 'mb-3' });
    var label = el('label', { 'class': 'form-label' + (f.required ? ' required' : ''), 'for': id }, f.label);
    var input;
    if (f.multi && f.options) {
      // Compose profiles: grouped checkbox cards → CSV on submit.
      return buildProfilePicker(wrap, label, id, f);
    }
    wrap.appendChild(label);
    if (f.options) {
      input = el('select', { 'class': 'form-select', id: id, name: f.name });
      if (!f.required) { input.appendChild(el('option', { value: '' }, '(default)')); }
      f.options.forEach(function (o) {
        var opt = el('option', { value: o }, (f.labels && f.labels[o]) || o);
        if (o === f.value) { opt.selected = true; }
        input.appendChild(opt);
      });
      if (f.required && f.options.length === 0) {
        input.appendChild(el('option', { value: '' }, '(none available)'));
      }
    } else {
      input = el('input', { 'class': 'form-control', id: id, name: f.name, autocomplete: 'off', spellcheck: 'false' });
      if (f.placeholder) { input.setAttribute('placeholder', f.placeholder); }
      if (f.value) { input.value = f.value; }
      if (f.type) { input.setAttribute('type', f.type); }
    }
    if (f.required) { input.required = true; }
    wrap.appendChild(input);
    return wrap;
  }

  function openForm(d) {
    if (!modalEl) { return; }
    current = d;
    modalEl.querySelector('[data-action-title]').textContent = d.title;
    var cmd = modalEl.querySelector('[data-action-command]');
    cmd.textContent = d.command || '';
    cmd.parentElement.classList.toggle('d-none', !d.command);
    var soft = modalEl.querySelector('[data-action-soft]');
    soft.classList.toggle('d-none', !d.soft);
    var softText = soft.querySelector('[data-action-soft-text]');
    if (softText) {
      softText.textContent = d.softMessage || 'This changes the running stack. Review the command, then confirm.';
    }
    modalEl.querySelector('[data-action-danger]').classList.toggle('d-none', !d.danger);
    var fields = modalEl.querySelector('[data-action-fields]');
    fields.textContent = '';
    var fixed = Object.keys(d.fixed || {});
    if (fixed.length) {
      var dl = el('dl', { 'class': 'row mb-3' });
      fixed.forEach(function (k) {
        dl.appendChild(el('dt', { 'class': 'col-4 text-secondary fw-normal' }, k));
        dl.appendChild(el('dd', { 'class': 'col-8 mdb-mono mb-1' }, d.fixed[k]));
      });
      fields.appendChild(dl);
    }
    (d.fields || []).forEach(function (f) { fields.appendChild(field(f)); });
    var confirm = modalEl.querySelector('[data-action-confirm]');
    var confirmInput = confirm.querySelector('input');
    confirmInput.value = '';
    confirm.classList.toggle('d-none', !d.confirmHint);
    confirm.querySelector('[data-action-confirm-hint]').textContent = d.confirmHint || '';
    confirmInput.required = !!d.confirmHint;
    var status = modalEl.querySelector('[data-action-status]');
    status.className = 'modal-status ' + (d.danger ? 'bg-danger' : 'bg-primary');
    var submit = modalEl.querySelector('[data-action-submit]');
    submit.className = 'btn ' + (d.danger ? 'btn-danger' : 'btn-primary');
    submit.textContent = d.submitLabel || (d.danger ? 'I understand, run it' : (d.soft ? 'Confirm and run' : 'Run'));
    submit.disabled = false;
    setModalLocked(false);
    showError('');
    var m = modal();
    if (m) { m.show(); }
    var first = fields.querySelector('input, select') || (d.confirmHint ? confirmInput : submit);
    setTimeout(function () { first.focus(); }, 200);
  }

  if (modalEl) {
    modalEl.addEventListener('hide.bs.modal', function (e) {
      if (!modalBusy) { return; }
      e.preventDefault();
      toast('Wait for the action to start before closing', 'warning');
    });
  }

  if (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      if (!current || modalBusy) { return; }
      var values = {};
      (current.fields || []).forEach(function (f) {
        var v = form.elements[f.name].value.trim();
        if (v !== '') { values[f.name] = v; }
      });
      var missing = (current.fields || []).filter(function (f) { return f.required && !values[f.name]; });
      if (missing.length) {
        showError(missing[0].label + ' is required.');
        return;
      }
      var confirm = (form.elements.__confirm && form.elements.__confirm.value) || '';
      if (current.confirmHint) {
        if (!confirm) {
          showError('Type the confirmation value to continue.');
          return;
        }
        var want = current.confirmValue;
        if (want === undefined || want === null || want === '') {
          want = expectedConfirm(current.confirmHint, Object.assign({}, current.fixed || {}, values));
        }
        if (want && confirm !== want) {
          showError('Type ' + want + ' exactly to confirm.');
          return;
        }
      }
      var submit = modalEl.querySelector('[data-action-submit]');
      var submitLabel = submit.textContent;
      submit.disabled = true;
      submit.setAttribute('data-mdb-busy-label', submitLabel);
      submit.textContent = 'Starting…';
      setModalLocked(true);
      setPageBusy(true, 'Starting ' + (current.title || 'action') + '…');
      current.submit(values, confirm).then(function () {
        setModalLocked(false);
        setPageBusy(false);
        var m = modal();
        if (m) { m.hide(); }
      }, function (err) {
        setModalLocked(false);
        setPageBusy(false);
        submit.disabled = false;
        submit.textContent = submit.getAttribute('data-mdb-busy-label') || submitLabel;
        submit.removeAttribute('data-mdb-busy-label');
        showError(err.message);
        toast(err.message, 'danger');
        if (err.status === 409 && err.body && err.body.job) {
          var link = el('button', { type: 'button', 'class': 'btn btn-sm ms-2', 'data-job-open': err.body.job.id }, 'Show it');
          modalEl.querySelector('[data-action-error]').appendChild(link);
        }
      });
    });
  }

  // expectedConfirm maps the server's confirm_hint + known params to the
  // typed value the CLI and API will check again.
  function expectedConfirm(hint, params) {
    if (hint === 'ROTATE') { return 'ROTATE'; }
    if (hint === 'the project name') { return params.name || ''; }
    if (hint === 'the database name') { return params.db || ''; }
    if (hint === 'the environment name') { return params.name || ''; }
    if (hint === 'the volume name') { return params.volume || ''; }
    if (hint === 'the node name') { return params.node || ''; }
    if (hint === 'the cluster name') { return params.cluster || ''; }
    return '';
  }

  // --- Actions -------------------------------------------------------------

  var choiceKinds = { engine: true, image: true, service: true, volume: true, dump: true, 'env-type': true, 'env-name': true };
  var fixedOptions = { 'pitr-type': ['full', 'diff', 'incr'] };

  // stoppedLabels marks the engines that are not running; the API lists
  // them after the running ones, so a form defaults to an engine that is up.
  function stoppedLabels(choices) {
    var running = choices.running || [];
    var labels = {};
    (choices.engine || []).forEach(function (e) {
      if (running.indexOf(e) < 0) { labels[e] = e + ' (not running)'; }
    });
    return labels;
  }

  // softMessage is the confirm-modal warning for operate actions that are
  // not destructive. ConfirmAll sets a.confirm for those; ConfirmDestructive
  // leaves it false so they run without a soft modal.
  function softMessage(name, preset) {
    if (name === 'config.set' && preset && preset.key === 'MDB_PUBLISH') {
      return 'Writes MDB_PUBLISH=' + (preset.value || '') + ' into .env. Host ports change only after you run Start to apply (Compose republish). Review the command, then confirm.';
    }
    if (name === 'config.set') {
      return 'Writes one key into the instance .env. Some keys need Start (or another action) before the running stack matches. Review the command, then confirm.';
    }
    if (name === 'service.restart') {
      return 'Restart briefly disconnects clients on that service. Review the command, then confirm.';
    }
    if (name === 'stack.down' || name === 'fixture.down') {
      return 'Stopping takes the stack offline. Review the command, then confirm.';
    }
    if (name === 'fixture.up') {
      return 'Starts the disposable fixture engines used by tests. This can take a minute while images start. Progress streams in the job drawer after you confirm.';
    }
    if (name === 'stack.up') {
      return 'This starts containers for the selected profiles. Review the command, then confirm.';
    }
    if (name === 'stack.start') {
      return 'This boots the stack and may provision a project. It does not open a second dashboard. Review the command, then confirm.';
    }
    if (name === 'backup.restore' || name.indexOf('destroy.') === 0) {
      return 'This changes the running stack. Review the command, then confirm.';
    }
    return 'This changes the running stack. Review the command, then confirm.';
  }

  // runAction asks the API what the action needs now. Filled-in reads run at
  // once; ConfirmAll soft-confirms writes; destructive always needs a typed
  // value. Missing parameters are collected in the same modal.
  // trigger is the clicked [data-action] button (optional) for busy UI.
  function runAction(name, preset, trigger) {
    preset = preset || {};
    if (trigger) {
      busyTrigger = trigger;
      setButtonBusy(trigger, true, 'Preparing…');
    }
    setPageBusy(true, 'Preparing ' + name.replace(/\./g, ' ') + '…');
    Promise.all([api('GET', '/api/v1/actions'), api('GET', '/api/v1/choices').catch(function () { return {}; })]).then(function (r) {
      var a = r[0].filter(function (x) { return x.name === name; })[0];
      var choices = r[1];
      if (!a) { throw new Error('no action ' + name); }
      rememberTitles(r[0]);
      if (!a.allowed) {
        clearTriggerBusy();
        setPageBusy(false);
        toast(a.reason || 'Not available', 'danger');
        openForm({ title: a.title, command: a.command, fields: [], submitLabel: 'Close', submit: function () { return Promise.resolve(); } });
        showError('Not available: ' + a.reason + '.');
        modalEl.querySelector('[data-action-submit]').disabled = true;
        return;
      }
      var missing = (a.params || []).filter(function (p) { return !(p.name in preset); });
      var command = a.command;
      Object.keys(preset).forEach(function (k) { command = command.split('<' + k + '>').join(preset[k]); });
      var needTyped = !!a.destructive;
      var needSoft = !!a.confirm && !needTyped;
      var submit = function (values, confirm) {
        var params = {};
        Object.keys(preset).forEach(function (k) { params[k] = preset[k]; });
        Object.keys(values).forEach(function (k) { params[k] = values[k]; });
        if (needTyped && !confirm) {
          return Promise.reject(new Error('Confirmation is required.'));
        }
        return api('POST', '/api/v1/jobs', { action: name, params: params, confirm: confirm || '' }).then(function (job) {
          job.title = a.title;
          toast('Started: ' + a.title, 'success');
          clearTriggerBusy();
          openJob(job.id, job);
          refreshPage();
        });
      };
      if (missing.length === 0 && !needSoft && !needTyped) {
        setBusyLabel('Starting ' + a.title + '…');
        if (trigger) { setButtonBusy(trigger, true, 'Starting…'); }
        submit({}, '').then(function () {
          setPageBusy(false);
        }).catch(function (err) {
          clearTriggerBusy();
          setPageBusy(false);
          toast(err.message, 'danger');
          openForm({ title: a.title, command: command, fields: [], submit: submit });
          showError(err.message);
        });
        return;
      }
      // Modal takes over: clear page dim so the dialog is usable, restore the
      // trigger label (the confirm button will show Starting… on submit).
      clearTriggerBusy();
      setPageBusy(false);
      openForm({
        title: a.title,
        command: command,
        fixed: preset,
        danger: needTyped,
        soft: needSoft,
        softMessage: softMessage(name, preset),
        confirmHint: needTyped ? (a.confirm_hint || '') : '',
        confirmValue: needTyped ? expectedConfirm(a.confirm_hint || '', preset) : undefined,
        submitLabel: needTyped ? 'I understand, run it' : (needSoft ? 'Confirm and run' : 'Run'),
        fields: missing.map(function (p) {
          var isProfiles = p.kind === 'profiles' || (name === 'config.set' && preset.key === 'MDB_PROFILES' && p.name === 'value');
          var opts = choiceKinds[p.kind] ? (choices[p.kind] || []) : fixedOptions[p.kind];
          if (isProfiles) { opts = choices.profile || choices.profiles || opts || []; }
          return {
            name: p.name, label: isProfiles ? 'Profiles' : p.label, required: p.required,
            options: opts,
            multi: !!isProfiles,
            value: isProfiles ? (preset.value || preset.profiles || '') : (preset[p.name] || ''),
            hint: isProfiles ? 'Select engines and stack extras. Empty keeps the configured MDB_PROFILES.' : '',
            labels: p.kind === 'engine' ? stoppedLabels(choices) : null,
            engines: isProfiles ? (choices.engine || []) : null,
            active: isProfiles ? (choices.active_profiles || []) : null,
            placeholder: p.kind === 'checks' ? '21,28' : ''
          };
        }),
        submit: submit
      });
    }).catch(function (err) {
      clearTriggerBusy();
      setPageBusy(false);
      toast(err.message, 'danger');
      openForm({ title: name, fields: [], submit: function () { return Promise.resolve(); } });
      showError(err.message);
    });
  }

  // --- The job drawer ------------------------------------------------------

  var drawerEl = document.getElementById('mdb-job');
  var stream = null;
  var ticker = null;
  var actionTitles = {};
  // jobLocked keeps the drawer open while a command is running so progress
  // and the EventSource stay attached (closing mid-run looked like cancel).
  var jobLocked = false;
  var JOB_KEY = 'dbpantry-running-job';
  window.mdbJobLocked = function () { return jobLocked; };

  function rememberJob(id, meta) {
    try {
      if (!id) { sessionStorage.removeItem(JOB_KEY); return; }
      sessionStorage.setItem(JOB_KEY, JSON.stringify({ id: id, title: (meta && meta.title) || '', at: Date.now() }));
    } catch (err) { /* storage blocked */ }
  }

  function clearRememberedJob() {
    try { sessionStorage.removeItem(JOB_KEY); } catch (err) { /* storage blocked */ }
  }

  function rememberTitles(list) {
    (list || []).forEach(function (a) {
      if (a && a.name) { actionTitles[a.name] = a.title || a.name; }
    });
  }

  function jobTitle(job) {
    return job.title || actionTitles[job.action] || job.action || 'Job';
  }

  // --- The job drawer: run snapshots ----------------------------------------
  //
  // A formatted command run as a job (--pretty --events) prints a run
  // snapshot line, RUN_PREFIX + JSON, whenever its steps change: the steps,
  // their resources and states, start/end times, how it failed, and what to
  // do next. The drawer renders the latest snapshot as one row per step,
  // updated in place, and keeps every other line as the raw log. Commands
  // without snapshots (verify, backups) open on the raw log.

  var RUN_PREFIX = '::dbpantry-run:: ';
  var SVG_NS = 'http://www.w3.org/2000/svg';
  var ICON_PATHS = {
    done: '<path d="M20 6 9 17l-5-5"/>',
    failed: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
    skipped: '<path d="M6 12h12"/>',
    warning: '<path d="M12 4 2.5 20h19z"/><path d="M12 10v4"/><path d="M12 17v.01"/>',
    pending: '<circle cx="12" cy="12" r="7"/>',
    note: '<circle cx="12" cy="12" r="2" fill="currentColor"/>'
  };
  var currentJob = null;
  var currentJobId = null;
  var currentJobAction = null;
  var run = null;
  var rawLines = [];
  var pickedView = null;
  var jobQuery = '';
  var jobLevel = 'all';
  var showTimes = true;
  var following = true;
  var expanded = {};
  var renderQueued = false;

  function jobEl(sel) { return drawerEl ? drawerEl.querySelector(sel) : null; }

  function pad2(n) { return (n < 10 ? '0' : '') + n; }

  function fmtDur(ms) {
    if (!(ms >= 0)) { ms = 0; }
    if (ms < 1000) { return Math.round(ms) + 'ms'; }
    var s = ms / 1000;
    if (s < 10) { return s.toFixed(1) + 's'; }
    if (s < 60) { return Math.floor(s) + 's'; }
    var m = Math.floor(s / 60);
    if (m < 60) { return m + 'm ' + pad2(Math.floor(s % 60)) + 's'; }
    return Math.floor(m / 60) + 'h ' + pad2(m % 60) + 'm';
  }

  function fmtBytes(n) {
    var units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    var i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + units[i];
  }

  function clockOf(t) {
    var d = new Date(t);
    return pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds()) + '.' + pad2(Math.floor(d.getMilliseconds() / 10));
  }

  function nodeDur(n, now) {
    if (!n.started) { return ''; }
    if (!n.ended && n.state !== 'running') { return ''; }
    return fmtDur((n.ended || now) - n.started);
  }

  function isIssue(state) { return state === 'failed' || state === 'warning' || state === 'skipped'; }

  function plural(n, word) {
    var w = String(word || '').toLowerCase();
    if (n === 1) { w = w.replace(/s$/, ''); }
    return n + ' ' + w;
  }

  function stateIcon(state) {
    if (state === 'running') {
      return el('span', { 'class': 'mdb-run-spin', role: 'img', 'aria-label': 'running' });
    }
    var svg = document.createElementNS(SVG_NS, 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('width', '14');
    svg.setAttribute('height', '14');
    svg.setAttribute('fill', 'none');
    svg.setAttribute('stroke', 'currentColor');
    svg.setAttribute('stroke-width', state === 'done' || state === 'failed' ? '3' : '2.5');
    svg.setAttribute('stroke-linecap', 'square');
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', state);
    svg.innerHTML = ICON_PATHS[state] || ICON_PATHS.pending;
    return svg;
  }

  // highlight appends text to node with the search query marked.
  function highlight(node, text) {
    text = String(text || '');
    var q = jobQuery.trim().toLowerCase();
    if (!q) { node.appendChild(document.createTextNode(text)); return; }
    var lower = text.toLowerCase();
    var i = 0;
    while (i < text.length) {
      var hit = lower.indexOf(q, i);
      if (hit < 0) { node.appendChild(document.createTextNode(text.slice(i))); break; }
      if (hit > i) { node.appendChild(document.createTextNode(text.slice(i, hit))); }
      node.appendChild(el('mark', { 'class': 'mdb-run-hit' }, text.slice(hit, hit + q.length)));
      i = hit + q.length;
    }
  }

  function matchQ(text) {
    var q = jobQuery.trim().toLowerCase();
    return !q || String(text || '').toLowerCase().indexOf(q) >= 0;
  }

  function filtering() { return !!jobQuery.trim() || jobLevel !== 'all'; }

  function jobActive() {
    return !!currentJob && (currentJob.state === 'running' || currentJob.state === 'paused');
  }

  // Steps is the default view for every job; Raw log only when picked.
  function effectiveView() { return pickedView || 'steps'; }

  // fallbackRun is the Steps view of a command that prints no run snapshot
  // (restart, config validate, …): one step for the job, its output lines as
  // rows, so Steps never sits empty or disabled.
  function fallbackRun() {
    var job = currentJob || {};
    var state = { running: 'running', paused: 'running', succeeded: 'done', failed: 'failed', cancelled: 'skipped' }[job.state] || 'running';
    var started = job.started ? Date.parse(job.started) : (rawLines[0] && rawLines[0].t) || Date.now();
    var kids = [];
    rawLines.forEach(function (l) {
      var text = String(l.text || '').replace(/\s+$/, '');
      if (!text.trim()) { return; }
      var level = rawLevel(text);
      kids.push({
        label: text.replace(/^\s*[✔✖·…⚠]\s?/, ''),
        state: { err: 'failed', warn: 'warning', ok: 'done' }[level] || 'note',
        line: true
      });
    });
    return {
      title: (job.args || []).join(' '),
      facts: [],
      state: state === 'running' ? 'running' : state,
      steps: [{
        label: jobTitle(job),
        state: state,
        detail: state === 'skipped' ? 'stopped' : '',
        started: started,
        ended: job.ended ? Date.parse(job.ended) : null,
        children: kids
      }]
    };
  }

  function viewRun() { return run || fallbackRun(); }

  // runProgress is the whole run's progress: finished steps plus the
  // running step's own fraction, and that step's estimate of time left.
  function runProgress() {
    if (!run || !run.steps || !run.steps.length) { return null; }
    var done = 0;
    var eta = 0;
    var step = null;
    run.steps.forEach(function (s) {
      if (s.state === 'done' || s.state === 'warning' || s.state === 'skipped') { done += 1; }
      else if (s.state === 'running') {
        step = s;
        if (s.pct != null) { done += s.pct / 100; }
        if (s.eta) { eta = s.eta; }
      }
    });
    return { pct: Math.floor(done / run.steps.length * 100), eta: eta, step: step };
  }

  // settleRun closes a snapshot the command never finished (killed, aborted):
  // nothing may keep spinning once the job is over.
  function settleRun(failed) {
    if (!run) { return; }
    var now = Date.now();
    (function walk(nodes) {
      (nodes || []).forEach(function (n) {
        walk(n.children);
        if (n.state === 'running' || n.state === 'pending') {
          n.state = 'skipped';
          n.detail = n.started ? 'stopped' : 'not started';
          if (n.started) { n.ended = n.ended || now; }
          n.pct = null;
        }
      });
    })(run.steps);
    if (run.state === 'running') { run.state = failed ? 'failed' : 'done'; }
  }

  function renderFacts() {
    var box = jobEl('[data-job-facts]');
    if (!box) { return; }
    box.textContent = '';
    var add = function (k, v, cls) {
      var f = el('div', { 'class': 'datagrid-item' });
      f.appendChild(el('div', { 'class': 'datagrid-title' }, k));
      var val = el('div', { 'class': 'datagrid-content' + (cls ? ' ' + cls : '') });
      if (v && v.nodeType) { val.appendChild(v); } else { val.textContent = v || '—'; }
      f.appendChild(val);
      box.appendChild(f);
      return val;
    };
    var job = currentJob || {};
    add('Status', statusTag(job));
    var d = add('Duration', job.started ? fmtDur((job.ended ? new Date(job.ended) : new Date()) - new Date(job.started)) : '—', 'mdb-tabular');
    d.setAttribute('data-job-elapsed', '');
    var facts = {};
    var notes = [];
    ((run && run.facts) || []).forEach(function (f) {
      if (f[0]) { facts[f[0]] = f[1]; } else { notes.push(f[1]); }
    });
    if (facts.profiles) { add('Profiles', facts.profiles.split(',').join(', ')); }
    if (facts.backend || facts.publish) { add('Backend · publish', (facts.backend || '—') + ' · ' + (facts.publish || '—')); }
    if (notes.length) { add('Note', notes.join(' · ')); }
    if (!run) {
      if (job.started) { add('Started', new Date(job.started).toLocaleTimeString()); }
      if (job.actor) { add('By', job.actor); }
    }
  }

  // statusTag is the job's state as the same badge the Jobs table and the
  // health column use: a light Tabler badge with a status dot.
  function statusTag(job) {
    var p = runProgress();
    var tone = 'secondary', text = 'Starting…', live = false;
    switch (job.state) {
      case 'running':
        tone = 'azure'; live = true;
        text = 'Running' + (p ? ' · ' + p.pct + '%' : '') + (p && p.eta ? ' · ETA ' + fmtDur(p.eta * 1000) : '');
        break;
      case 'paused': tone = 'yellow'; text = 'Paused'; break;
      case 'succeeded': tone = 'green'; text = 'Succeeded · exit 0'; break;
      case 'cancelled': tone = 'yellow'; text = 'Aborted'; break;
      case 'failed': tone = 'red'; text = 'Failed · exit ' + (job.exit_code || 1); break;
      default: live = true;
    }
    var t = el('span', { 'class': 'badge bg-' + tone + '-lt mdb-run-tag text-nowrap' });
    t.appendChild(el('span', { 'class': 'status-dot status-' + tone + ' me-1' + (live ? ' status-dot-animated' : '') }));
    t.appendChild(document.createTextNode(text));
    return t;
  }

  function renderSegments() {
    var box = jobEl('[data-job-segments]');
    if (!box) { return; }
    box.textContent = '';
    var steps = (run && run.steps) || [];
    box.classList.toggle('d-none', steps.length === 0);
    var now = Date.now();
    steps.forEach(function (s, i) {
      var seg = el('div', { 'class': 'mdb-job-seg is-' + s.state, title: s.label + (s.detail ? ' · ' + s.detail : '') });
      var bar = el('div', { 'class': 'mdb-job-seg-bar' });
      if (s.state === 'running') {
        var fill = el('span');
        fill.style.width = (s.pct != null ? s.pct : 0) + '%';
        bar.appendChild(fill);
        if (s.pct == null) { bar.classList.add('is-indeterminate'); }
      }
      seg.appendChild(bar);
      var meta = el('div', { 'class': 'mdb-job-seg-meta' });
      meta.appendChild(el('span', { 'class': 'mdb-job-seg-n' }, pad2(i + 1)));
      meta.appendChild(el('span', { 'class': 'mdb-job-seg-name' }, s.label));
      meta.appendChild(el('span', { 'class': 'mdb-job-seg-dur' }, nodeDur(s, now)));
      seg.appendChild(meta);
      box.appendChild(seg);
    });
  }

  // counts tallies leaves by state under nodes.
  function counts(nodes, acc) {
    acc = acc || { failed: 0, skipped: 0, done: 0, total: 0, notStarted: 0, stopped: 0, groups: {} };
    (nodes || []).forEach(function (n) {
      if (n.children && n.children.length) { counts(n.children, acc); return; }
      acc.total++;
      if (n.state === 'failed') { acc.failed++; }
      if (n.state === 'done') { acc.done++; }
      if (n.state === 'skipped') {
        acc.skipped++;
        if (n.detail === 'stopped') { acc.stopped++; } else { acc.notStarted++; }
      }
    });
    return acc;
  }

  function lastErrorLine(lines) {
    for (var i = (lines || []).length - 1; i >= 0; i--) {
      var l = String(lines[i] || '').trim();
      if (l) { return l; }
    }
    return '';
  }

  function renderBanner() {
    var box = jobEl('[data-job-banner]');
    if (!box) { return; }
    box.textContent = '';
    box.className = 'mdb-job-banner d-none';
    var job = currentJob || {};
    var icon = function (paths) {
      var i = el('span', { 'aria-hidden': 'true' });
      i.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" class="icon alert-icon" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' + paths + '</svg>';
      return i.firstChild;
    };
    if (job.state === 'failed') {
      var f = (run && run.failure) || null;
      box.className = 'alert alert-danger mdb-job-banner is-fail';
      box.appendChild(icon('<path d="M12 9v4"/><path d="M10.363 3.591l-8.106 13.534a1.914 1.914 0 0 0 1.636 2.871h16.214a1.914 1.914 0 0 0 1.636 -2.87l-8.106 -13.536a1.914 1.914 0 0 0 -3.274 0z"/><path d="M12 16h.01"/>'));
      var text = el('div', { 'class': 'mdb-job-banner-text' });
      var kicker = 'Failed';
      if (f && f.index) { kicker += ' at step ' + f.index + ' · ' + f.step; }
      text.appendChild(el('div', { 'class': 'subheader text-danger' }, kicker));
      var headline = (f && f.headline) || lastRawIssue() || ('exit ' + (job.exit_code || 1));
      text.appendChild(el('h4', { 'class': 'alert-heading mdb-job-banner-title' }, headline));
      var why = 'The run stopped and exited with code ' + (job.exit_code || (f && f.exit) || 1) + '.';
      if (f && f.index && run.steps[f.index - 1]) {
        var c = counts(run.steps[f.index - 1].children);
        if (c.notStarted) { why += ' ' + plural(c.notStarted, 'waiting resources') + (c.notStarted === 1 ? ' was' : ' were') + ' not started.'; }
        if (c.stopped) { why += ' ' + plural(c.stopped, 'resources') + (c.stopped === 1 ? ' was' : ' were') + ' stopped mid-way.'; }
      }
      text.appendChild(el('div', { 'class': 'alert-description' }, why));
      if (f && f.lines && f.lines.length) {
        text.appendChild(el('code', { 'class': 'mdb-job-banner-code' }, f.what + ' → ' + lastErrorLine(f.lines)));
      }
      var acts = el('div', { 'class': 'btn-list mt-2' });
      var retry = el('button', { type: 'button', 'class': 'btn btn-primary btn-sm', 'data-job-retry': '', title: 'Run the same command again' });
      retry.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" class="icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg> Retry';
      acts.appendChild(retry);
      if (f && f.service) {
        var logs = el('a', { 'class': 'btn btn-sm', href: '/services/' + encodeURIComponent(f.service) + '/logs', title: 'Open the ' + f.service + ' log' });
        logs.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m4 17 6-6-6-6"/><path d="M12 19h8"/></svg> ';
        logs.appendChild(document.createTextNode(f.service + ' logs'));
        acts.appendChild(logs);
      }
      text.appendChild(acts);
      box.appendChild(text);
      return;
    }
    if (job.state === 'cancelled') {
      box.className = 'alert alert-warning mdb-job-banner is-warn';
      box.appendChild(icon('<path d="M3 12a9 9 0 1 0 18 0a9 9 0 0 0 -18 0"/><path d="M12 8v4"/><path d="M12 16h.01"/>'));
      var t2 = el('div', { 'class': 'mdb-job-banner-text' });
      t2.appendChild(el('h4', { 'class': 'alert-heading mdb-job-banner-title' }, 'Aborted'));
      t2.appendChild(el('div', { 'class': 'alert-description' }, 'Stopped by an operator. Restart runs the same command again.'));
      box.appendChild(t2);
      return;
    }
    if (job.state === 'succeeded') {
      box.className = 'alert alert-success mdb-job-banner is-done';
      box.appendChild(icon('<path d="M5 12l5 5l10 -10"/>'));
      var t3 = el('div', { 'class': 'mdb-job-banner-text' });
      var titles = { up: 'Stack is up', down: 'Stack is down', 'project new': 'Project provisioned' };
      var head = (run && titles[run.title]) || (jobTitle(job) + ' finished');
      t3.appendChild(el('h4', { 'class': 'alert-heading mdb-job-banner-title' }, head));
      var bits = [];
      var last = run && run.steps && run.steps[run.steps.length - 1];
      if (last && last.detail) { bits.push(last.detail); }
      if (run && run.connect && run.connect.length) {
        bits.push(plural(run.connect.length, 'engines') + ' reachable from containers');
        var published = run.connect.filter(function (r) { return !!r.host; }).length;
        bits.push(published ? published + ' published to this machine' : 'nothing published to this machine');
      }
      if (bits.length) { t3.appendChild(el('div', { 'class': 'alert-description' }, bits.join(' · '))); }
      box.appendChild(t3);
    }
  }

  function lastRawIssue() {
    for (var i = rawLines.length - 1; i >= 0; i--) {
      if (rawLevel(rawLines[i].text) === 'err') { return rawLines[i].text.replace(/^\s*[✖✔·…]\s*/, ''); }
    }
    return '';
  }

  // flatRows lays the snapshot out as rows: steps, then (when open) their
  // groups and resources, each knowing its ancestors for filtering.
  function flatRows(r) {
    var rows = [];
    var filt = filtering();
    (r.steps || []).forEach(function (st, i) {
      var key = i + ':' + st.label;
      var has = !!(st.children && st.children.length);
      var defOpen = st.state === 'running' || st.state === 'failed' || st.state === 'warning' || !st.detail;
      var open = has && (filt || (Object.prototype.hasOwnProperty.call(expanded, key) ? expanded[key] : defOpen));
      var si = rows.length;
      rows.push({ kind: 'step', node: st, key: key, has: has, open: open, parents: [], lasts: [] });
      if (!open) { return; }
      (function walk(nodes, parents, lasts) {
        nodes.forEach(function (n, j) {
          var isLast = j === nodes.length - 1;
          var idx = rows.length;
          var kids = !!(n.children && n.children.length);
          rows.push({ kind: kids ? 'group' : 'leaf', node: n, parents: parents, lasts: lasts.concat([isLast]) });
          if (kids) { walk(n.children, parents.concat([idx]), lasts.concat([isLast])); }
        });
      })(st.children, [si], []);
    });
    return rows;
  }

  // issueTotal counts each problem once: a parent that failed because a
  // child did is the same issue as that child.
  function issueTotal(nodes) {
    var n = 0;
    (nodes || []).forEach(function (x) {
      var below = issueTotal(x.children);
      n += below || (isIssue(x.state) ? 1 : 0);
    });
    return n;
  }

  function metaClass(state) {
    return { done: 'ok', failed: 'err', skipped: 'warn', warning: 'warn', running: 'info' }[state] || 'dim';
  }

  function metaText(row) {
    var n = row.node;
    if (row.kind === 'step') { return ''; }
    if (row.kind === 'group' || n.line) { return ''; }
    if (n.state === 'skipped') { return (n.detail || 'not started').replace(/^./, function (c) { return c.toUpperCase(); }); }
    if (n.state === 'done') { return n.status || n.detail || 'Done'; }
    if (n.state === 'failed') { return n.detail || n.status || 'Failed'; }
    return n.detail || n.status || (n.state === 'pending' ? 'Queued' : '');
  }

  function textBar(pct, width) {
    var full = Math.max(0, Math.min(width, Math.floor(pct / 100 * width)));
    var s = el('span', { 'class': 'mdb-run-textbar', 'aria-hidden': 'true' });
    s.appendChild(el('span', { 'class': 'is-fill' }, new Array(full + 1).join('━')));
    s.appendChild(el('span', { 'class': 'is-rest' }, new Array(width - full + 1).join('━')));
    return s;
  }

  function barCell(row, now) {
    var n = row.node;
    var cell = el('span', { 'class': 'mdb-run-bar' });
    if (row.kind === 'group') {
      var c = counts(n.children);
      var filled = c.failed || c.done;
      var cells = el('span', { 'class': 'mdb-run-cells is-' + (c.failed ? 'err' : n.state === 'running' ? 'info' : 'ok'), 'aria-hidden': 'true' });
      cells.appendChild(el('span', { 'class': 'is-fill' }, new Array(Math.min(filled, 24) + 1).join('■')));
      cells.appendChild(el('span', { 'class': 'is-rest' }, new Array(Math.min(c.total - filled, 24 - Math.min(filled, 24)) + 1).join('■')));
      cell.appendChild(cells);
      cell.appendChild(el('span', { 'class': 'mdb-run-cells-label' }, c.failed ? c.failed + ' failed' : c.done + '/' + c.total));
      return cell;
    }
    if (n.state !== 'running') { return cell; }
    if (n.pct != null) {
      var bar = el('span', { 'class': 'mdb-run-pbar', role: 'progressbar', 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': String(n.pct) });
      bar.appendChild(textBar(n.pct, row.kind === 'step' ? 12 : 10));
      bar.appendChild(el('span', { 'class': 'mdb-run-pct' }, n.pct + '%'));
      var info = '';
      if (n.bytes && n.total) { info = fmtBytes(n.total * n.pct / 100) + ' / ' + fmtBytes(n.total); }
      else if (n.total) { info = (n.done || 0) + '/' + n.total; }
      if (n.eta) { info += (info ? ' · ' : '') + 'ETA ' + fmtDur(n.eta * 1000); }
      if (info) { bar.appendChild(el('span', { 'class': 'mdb-run-barinfo' }, info)); }
      cell.appendChild(bar);
    } else if (n.started && now - n.started >= 3000) {
      cell.appendChild(el('span', { 'class': 'mdb-run-indet', role: 'progressbar', 'aria-label': 'working' }));
    }
    return cell;
  }

  function guides(row) {
    var g = el('span', { 'class': 'mdb-run-tree', 'aria-hidden': 'true' });
    var lasts = row.lasts || [];
    lasts.forEach(function (isLast, i) {
      var cls = i === lasts.length - 1 ? (isLast ? 'g-elbow' : 'g-tee') : (isLast ? 'g-blank' : 'g-pipe');
      g.appendChild(el('span', { 'class': 'mdb-run-g ' + cls }));
    });
    return g;
  }

  function renderRow(row, dim, now) {
    var n = row.node;
    var cls = 'mdb-run-row is-' + row.kind + ' st-' + n.state + (dim ? ' is-dim' : '') + (row.has ? ' is-toggle' : '') + (n.line ? ' is-line' : '');
    var r = el('div', { 'class': cls });
    if (row.has) {
      r.setAttribute('role', 'button');
      r.setAttribute('tabindex', '0');
      r.setAttribute('aria-expanded', row.open ? 'true' : 'false');
      r.setAttribute('data-run-toggle', row.key);
      r.title = row.open ? 'Fold this step' : 'Show what this step did';
    }
    var chev = el('span', { 'class': 'mdb-run-chev' + (row.has ? '' : ' is-none') + (row.open ? ' is-open' : '') });
    chev.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="m9 18 6-6-6-6"/></svg>';
    r.appendChild(chev);
    r.appendChild(guides(row));
    var ic = el('span', { 'class': 'mdb-run-icon' });
    ic.appendChild(stateIcon(n.state));
    r.appendChild(ic);
    var name = el('span', { 'class': 'mdb-run-name' });
    if (n.icon) { name.appendChild(el('span', { 'class': 'mdb-run-emoji', 'aria-hidden': 'true' }, n.icon)); }
    var label = n.label || '';
    if (row.kind === 'leaf') {
      var cut = label.lastIndexOf('/');
      if (cut > 0 && cut < label.length - 1) {
        var pre = el('span', { 'class': 'mdb-run-pre' });
        highlight(pre, label.slice(0, cut + 1));
        name.appendChild(pre);
        label = label.slice(cut + 1);
      }
    }
    var nm = el('span', { 'class': 'mdb-run-label' });
    highlight(nm, label);
    name.appendChild(nm);
    if (row.kind === 'step' && n.detail && !(n.state === 'running' && n.pct != null)) {
      var sum = el('span', { 'class': 'mdb-run-sum' });
      highlight(sum, n.detail);
      name.appendChild(sum);
    }
    name.title = n.label + (n.detail ? ' — ' + n.detail : '');
    r.appendChild(name);
    var mt = metaText(row);
    var meta = el('span', { 'class': 'mdb-run-meta is-' + metaClass(n.state) });
    if (mt) {
      meta.appendChild(el('i', { 'aria-hidden': 'true' }));
      var mtext = el('span');
      highlight(mtext, mt);
      meta.appendChild(mtext);
      meta.title = mt;
    }
    r.appendChild(meta);
    r.appendChild(barCell(row, now));
    r.appendChild(el('span', { 'class': 'mdb-run-dur' }, nodeDur(n, now)));
    return r;
  }

  function renderContext(box, r) {
    var line = el('div', { 'class': 'mdb-run-context' });
    line.appendChild(el('span', { 'class': 'mdb-run-emoji', 'aria-hidden': 'true' }, '⚡'));
    line.appendChild(el('b', null, 'dbpantry'));
    line.appendChild(document.createTextNode(' › '));
    line.appendChild(el('span', { 'class': 'is-info' }, r.title || ''));
    (r.facts || []).forEach(function (f, i) {
      line.appendChild(document.createTextNode(i === 0 ? '  ' : ' · '));
      if (f[0]) { line.appendChild(document.createTextNode(f[0] + ' ')); }
      line.appendChild(el('span', { 'class': f[0] === 'publish' ? 'is-warn' : 'is-info' }, f[1]));
    });
    box.appendChild(line);
  }

  function renderFailTail(box) {
    var f = run.failure;
    if (!f || !f.lines || !f.lines.length) { return; }
    var tail = el('div', { 'class': 'mdb-run-tail' });
    tail.appendChild(el('div', { 'class': 'mdb-run-rule' }, '── 💥 ' + f.what + ' said ' + new Array(Math.max(4, 44 - f.what.length) + 1).join('─')));
    f.lines.forEach(function (l) {
      var bad = /^\s*\{"error"|^\s*error\b/i.test(l);
      var row = el('div', { 'class': 'mdb-run-tail-line' + (bad ? ' is-err' : '') });
      if (bad) { row.appendChild(stateIcon('failed')); }
      var t = el('span');
      highlight(t, l);
      row.appendChild(t);
      tail.appendChild(row);
    });
    if (f.exit) { tail.appendChild(el('div', { 'class': 'mdb-run-exit' }, 'exit ' + f.exit)); }
    box.appendChild(tail);
  }

  function copyButton(text) {
    var b = el('button', { type: 'button', 'class': 'mdb-job-linkbtn', title: 'Copy: ' + text }, 'Copy');
    b.addEventListener('click', function (e) {
      e.stopPropagation();
      if (!navigator.clipboard) { toast('Select and copy', 'warning'); return; }
      navigator.clipboard.writeText(text).then(function () {
        b.textContent = 'Copied';
        setTimeout(function () { b.textContent = 'Copy'; }, 1400);
      }, function () { toast('Copy blocked', 'danger'); });
    });
    return b;
  }

  function renderOkTail(box) {
    if (run.connect && run.connect.length) {
      var c = el('div', { 'class': 'mdb-run-tail' });
      c.appendChild(el('div', { 'class': 'mdb-run-tail-title' }, '🔌 Connect'));
      var wrap = el('div', { 'class': 'table-responsive' });
      var t = el('table', { 'class': 'mdb-run-table' });
      var head = el('tr');
      ['Engine', 'From containers', 'From this machine', 'Pooled', 'TLS'].forEach(function (h) { head.appendChild(el('th', null, h)); });
      var thead = el('thead');
      thead.appendChild(head);
      t.appendChild(thead);
      var tb = el('tbody');
      run.connect.forEach(function (r) {
        var tr = el('tr');
        tr.appendChild(el('td', { 'class': 'is-strong' }, r.engine));
        var net = el('td');
        net.appendChild(el('span', { 'class': 'is-ok' }, r.network));
        net.appendChild(copyButton(r.network));
        tr.appendChild(net);
        var host = el('td', { 'class': r.host ? '' : 'is-dim' });
        if (r.host) {
          host.appendChild(el('span', { 'class': 'is-ok' }, r.host));
          host.appendChild(copyButton(r.host));
        } else {
          host.textContent = 'not published';
        }
        tr.appendChild(host);
        tr.appendChild(el('td', { 'class': r.pooled && !r.pooler_off ? '' : 'is-dim' }, r.pooled ? r.pooled + (r.pooler_off ? ' · off' : '') : '—'));
        tr.appendChild(el('td', { 'class': r.tls_always ? 'is-ok' : 'is-dim' }, r.tls_always ? 'always' : 'prod only'));
        tb.appendChild(tr);
      });
      t.appendChild(tb);
      wrap.appendChild(t);
      c.appendChild(wrap);
      box.appendChild(c);
    }
    if (run.next && run.next.length) {
      var nx = el('div', { 'class': 'mdb-run-tail' });
      nx.appendChild(el('div', { 'class': 'mdb-run-tail-title' }, '👉 Next'));
      run.next.forEach(function (it) {
        var row = el('div', { 'class': 'mdb-run-next' });
        row.appendChild(el('span', { 'class': 'is-dim' }, '›'));
        row.appendChild(el('span', { 'class': 'mdb-run-next-label' }, it.label));
        var cmd = el('span', { 'class': 'mdb-run-next-cmd' }, it.cmd);
        if (it.note) { cmd.appendChild(el('span', { 'class': 'is-dim' }, ' — ' + it.note)); }
        row.appendChild(cmd);
        row.appendChild(copyButton(it.cmd));
        nx.appendChild(row);
      });
      box.appendChild(nx);
    }
  }

  function renderSteps(now) {
    var box = jobEl('[data-job-steps]');
    if (!box) { return { hits: 0, issues: 0 }; }
    box.textContent = '';
    var r = viewRun();
    renderContext(box, r);
    var rows = flatRows(r);
    var filt = filtering();
    var keep = {};
    var hits = 0;
    rows.forEach(function (row, i) {
      var n = row.node;
      row.hit = matchQ(n.label + ' ' + (n.detail || '') + ' ' + (n.status || '')) && (jobLevel === 'all' || isIssue(n.state));
      if (row.hit) { hits++; }
      if (!filt || row.hit) {
        keep[i] = true;
        row.parents.forEach(function (p) { keep[p] = true; });
      }
    });
    rows.forEach(function (row, i) {
      if (keep[i]) { box.appendChild(renderRow(row, filt && !row.hit, now)); }
    });
    if (!filt && run) {
      if (run.failure) { renderFailTail(box); }
      if (run.state === 'done') { renderOkTail(box); }
    }
    return { hits: hits, issues: issueTotal(r.steps) };
  }

  var RAW_DUR = /\s+(\d+(?:\.\d+)?(?:ms|s)|\d+m\s?\d{2}s|\d+h\s?\d{2}m)$/;

  function rawLevel(text) {
    var t = String(text || '').trim();
    if (/^✖/.test(t) || /^exit [1-9]/.test(t) || /^\{"error"/.test(t) || /\b(error|failed|fatal|unhealthy)\b/i.test(t)) { return 'err'; }
    if (/^⚠/.test(t) || /\b(warn(ing)?|not started|stopped|aborted)\b/i.test(t)) { return 'warn'; }
    if (/^✔/.test(t) || t === 'exit 0') { return 'ok'; }
    return 'info';
  }

  function renderRaw() {
    var box = jobEl('#mdb-job-output');
    if (!box) { return { hits: 0, issues: 0 }; }
    box.textContent = '';
    var hasTimes = rawLines.some(function (l) { return !!l.t; });
    box.classList.toggle('has-times', hasTimes && showTimes);
    var hits = 0;
    var issues = 0;
    rawLines.forEach(function (l, i) {
      var level = rawLevel(l.text);
      var bad = level === 'err' || level === 'warn';
      if (bad) { issues++; }
      if (!((jobLevel === 'all' || bad) && matchQ(l.text))) { return; }
      hits++;
      var m = RAW_DUR.exec(l.text);
      var body = m ? l.text.slice(0, m.index) : l.text;
      body = body.replace(/^(\s*)[✔✖·…]\s?/, '$1').replace(/\s+$/, '');
      var row = el('div', { 'class': 'mdb-raw-row is-' + level });
      row.appendChild(el('span', { 'class': 'mdb-raw-n' }, String(i + 1)));
      row.appendChild(el('span', { 'class': 'mdb-raw-t' }, hasTimes && showTimes && l.t ? clockOf(l.t) : ''));
      row.appendChild(el('span', { 'class': 'mdb-raw-lv' }, { ok: 'OK', err: 'ERR', warn: 'WARN', info: 'INFO' }[level]));
      var msg = el('span', { 'class': 'mdb-raw-msg' });
      highlight(msg, body);
      row.appendChild(msg);
      row.appendChild(el('span', { 'class': 'mdb-raw-dur' }, m ? m[1] : ''));
      box.appendChild(row);
    });
    return { hits: hits, issues: issues, hasTimes: hasTimes };
  }

  function renderJob() {
    renderQueued = false;
    if (!drawerEl) { return; }
    var now = Date.now();
    var view = effectiveView();
    renderFacts();
    renderSegments();
    renderBanner();
    var stepsBox = jobEl('[data-job-steps]');
    var rawBox = jobEl('#mdb-job-output');
    var res = view === 'steps' ? renderSteps(now) : renderRaw();
    if (stepsBox) { stepsBox.classList.toggle('d-none', view !== 'steps'); }
    if (rawBox) { rawBox.classList.toggle('d-none', view !== 'raw'); }
    if (view === 'steps' && stepsBox) { stepsBox.classList.remove('d-none'); }
    drawerEl.querySelectorAll('[data-job-view]').forEach(function (b) {
      var on = b.getAttribute('data-job-view') === view;
      b.classList.toggle('active', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      if (b.getAttribute('data-job-view') === 'steps') {
        b.disabled = false;
        b.title = run ? 'One row per step, updated in place' : 'The job and each line it printed, as rows';
      }
    });
    var lines = jobEl('[data-job-lines]');
    if (lines) { lines.textContent = rawLines.length + (rawLines.length === 1 ? ' line' : ' lines'); }
    var issuesEl = jobEl('[data-job-issues]');
    if (issuesEl) {
      issuesEl.textContent = String(res.issues);
      issuesEl.classList.toggle('is-some', res.issues > 0);
    }
    var ts = jobEl('[data-job-ts]');
    if (ts) {
      ts.classList.toggle('d-none', view !== 'raw' || !res.hasTimes);
      ts.classList.toggle('active', showTimes);
      ts.setAttribute('aria-pressed', showTimes ? 'true' : 'false');
    }
    var filt = filtering();
    var count = jobEl('[data-job-count]');
    if (count) {
      count.textContent = filt ? res.hits + (res.hits === 1 ? ' match' : ' matches') : rawLines.length + (rawLines.length === 1 ? ' line' : ' lines');
      count.classList.toggle('is-hit', filt && res.hits > 0);
    }
    var none = jobEl('[data-job-noresults]');
    if (none) {
      none.classList.toggle('d-none', !(filt && res.hits === 0));
      var nt = jobEl('[data-job-noresults-text]');
      if (nt) { nt.textContent = jobQuery.trim() ? 'No lines match “' + jobQuery.trim() + '”' : 'No issues in this run'; }
    }
    var empty = jobEl('[data-job-empty]');
    if (empty) { empty.classList.toggle('d-none', view === 'steps' || rawLines.length > 0 || !jobActive()); }
    var followBtn = jobEl('[data-job-follow-btn]');
    if (followBtn) {
      followBtn.classList.toggle('d-none', !jobActive());
      followBtn.classList.toggle('btn-dark', following);
      followBtn.setAttribute('aria-pressed', following ? 'true' : 'false');
      var fl = followBtn.querySelector('[data-job-follow-label]');
      if (fl) { fl.textContent = following ? 'Following' : 'Jump to latest'; }
    }
    var plain = jobEl('#mdb-job-plain');
    if (plain) { plain.textContent = rawLines.map(function (l) { return l.text; }).join('\n'); }
    if (following && jobActive()) {
      var body = jobEl('[data-job-body]');
      if (body) { body.scrollTop = body.scrollHeight; }
    }
  }

  function queueRender() {
    if (renderQueued) { return; }
    renderQueued = true;
    (window.requestAnimationFrame || setTimeout)(renderJob);
  }

  function setJobControls(state) {
    if (!drawerEl) { return; }
    var active = state === 'running' || state === 'paused';
    var show = function (sel, on) {
      var n = drawerEl.querySelector(sel);
      if (n) { n.classList.toggle('d-none', !on); }
    };
    show('[data-job-pause]', state === 'running');
    show('[data-job-continue]', state === 'paused');
    show('[data-job-abort]', active);
    show('[data-job-restart]', !active && !!currentJobId && state !== 'failed');
    var rl = drawerEl.querySelector('[data-job-restart-label]');
    if (rl) { rl.textContent = state === 'failed' ? 'Retry' : 'Restart'; }
    var lockedText = drawerEl.querySelector('[data-job-locked-text]');
    if (lockedText) {
      lockedText.textContent = state === 'paused'
        ? 'Paused — continue or stop'
        : 'Running — drawer stays open';
    }
  }

  function jobControl(op) {
    if (!currentJobId) { return Promise.reject(new Error('no job')); }
    return api('POST', '/api/v1/jobs/' + encodeURIComponent(currentJobId) + '/control', { op: op }).then(function (job) {
      if (op === 'restart') {
        openJob(job.id, job);
        return job;
      }
      setJob(job);
      if (op === 'abort') {
        toast('Aborted', 'warning');
      }
      return job;
    });
  }

  function setDrawerLocked(locked) {
    jobLocked = !!locked;
    if (!drawerEl) { return; }
    drawerEl.setAttribute('data-mdb-locked', locked ? '1' : '0');
    var note = drawerEl.querySelector('[data-job-locked]');
    if (note) { note.classList.toggle('d-none', !locked); }
    var close = drawerEl.querySelector('[data-job-close]');
    if (close) {
      close.disabled = !!locked;
      close.title = locked
        ? 'Wait for the command to finish before closing'
        : 'Close the job drawer';
    }
  }

  window.addEventListener('beforeunload', function (e) {
    if (!jobLocked && !modalBusy) { return; }
    e.preventDefault();
    e.returnValue = '';
  });

  function resetJobOutput() {
    run = null;
    rawLines = [];
    pickedView = null;
    expanded = {};
    following = true;
    jobQuery = '';
    jobLevel = 'all';
    var search = jobEl('[data-job-search]');
    if (search) { search.value = ''; }
    drawerEl.querySelectorAll('[data-job-level]').forEach(function (b) {
      var on = b.getAttribute('data-job-level') === 'all';
      b.classList.toggle('active', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
  }

  function setJob(job) {
    currentJob = job;
    currentJobId = job.id || currentJobId;
    currentJobAction = job.action || currentJobAction;
    jobEl('[data-job-title]').textContent = jobTitle(job);
    jobEl('[data-job-kicker]').textContent = 'Run · ' + (job.actor || 'local');
    jobEl('[data-job-command]').textContent = job.args ? 'dbpantry ' + job.args.join(' ') : '';
    if (job.state && job.state !== 'running' && job.state !== 'paused') {
      settleRun(job.state !== 'succeeded');
    }
    setDrawerLocked(job.state === 'running' || job.state === 'paused');
    setJobControls(job.state);
    if (job.state === 'running' || job.state === 'paused') { rememberJob(job.id, job); }
    else { clearRememberedJob(); }
    clearInterval(ticker);
    if (job.state === 'running' || job.state === 'paused') {
      ticker = setInterval(queueRender, 1000);
    }
    queueRender();
  }

  function addJobLine(n, data) {
    var text = typeof data === 'string' ? data : (data && data.line) || '';
    var t = data && typeof data === 'object' ? data.t || 0 : 0;
    if (text.indexOf(RUN_PREFIX) === 0) {
      try { run = JSON.parse(text.slice(RUN_PREFIX.length)); } catch (err) { /* a torn line: keep the last snapshot */ }
      return;
    }
    rawLines.push({ n: n, text: text, t: t || Date.now() });
  }

  function openJob(id, known) {
    if (!drawerEl) { return; }
    if (stream) { stream.close(); stream = null; }
    resetJobOutput();
    currentJob = known || { id: id, state: 'running' };
    if (known) { setJob(known); }
    else {
      jobEl('[data-job-title]').textContent = 'Job';
      jobEl('[data-job-command]').textContent = '';
      setDrawerLocked(true);
      queueRender();
    }
    if (tabler.Offcanvas) { tabler.Offcanvas.getOrCreateInstance(drawerEl).show(); }
    stream = new EventSource('/api/v1/jobs/' + encodeURIComponent(id) + '/events');
    stream.addEventListener('line', function (e) {
      var data;
      try { data = JSON.parse(e.data); } catch (err) { data = e.data; }
      addJobLine(Number(e.lastEventId) || rawLines.length, data);
      queueRender();
    });
    stream.addEventListener('end', function (e) {
      stream.close();
      stream = null;
      var job = JSON.parse(e.data);
      if (known && known.title) { job.title = known.title; }
      setJob(job);
      var title = jobTitle(job);
      if (job.state === 'succeeded') { toast(title + ' finished', 'success'); }
      else if (job.state === 'cancelled') { toast(title + ' aborted', 'warning'); }
      else { toast(title + ' failed' + (job.exit_code ? ' (exit ' + job.exit_code + ')' : ''), 'danger'); }
      refreshPage();
    });
    stream.onerror = function () {
      if (stream && stream.readyState === EventSource.CLOSED) { stream = null; }
    };
    if (!known) {
      api('GET', '/api/v1/jobs/' + encodeURIComponent(id)).then(function (job) {
        job.lines = null;
        if (!actionTitles[job.action]) {
          api('GET', '/api/v1/actions').then(function (list) { rememberTitles(list); setJob(job); }, function () { setJob(job); });
        } else {
          setJob(job);
        }
      }, function () {});
    }
  }

  if (drawerEl) {
    drawerEl.addEventListener('hide.bs.offcanvas', function (e) {
      if (!jobLocked) { return; }
      e.preventDefault();
      toast('Wait for the command to finish before closing', 'warning');
    });
    drawerEl.addEventListener('hidden.bs.offcanvas', function () {
      if (stream) { stream.close(); stream = null; }
      clearInterval(ticker);
      setDrawerLocked(false);
    });
    drawerEl.addEventListener('click', function (e) {
      var v = e.target.closest('[data-job-view]');
      if (v && !v.disabled) { pickedView = v.getAttribute('data-job-view'); queueRender(); return; }
      var lv = e.target.closest('[data-job-level]');
      if (lv) {
        jobLevel = lv.getAttribute('data-job-level');
        drawerEl.querySelectorAll('[data-job-level]').forEach(function (b) {
          var on = b === lv;
          b.classList.toggle('active', on);
          b.setAttribute('aria-pressed', on ? 'true' : 'false');
        });
        queueRender();
        return;
      }
      if (e.target.closest('[data-job-ts]')) { showTimes = !showTimes; queueRender(); return; }
      if (e.target.closest('[data-job-reset]')) {
        jobQuery = '';
        jobLevel = 'all';
        var s = jobEl('[data-job-search]');
        if (s) { s.value = ''; }
        drawerEl.querySelectorAll('[data-job-level]').forEach(function (b) {
          var on = b.getAttribute('data-job-level') === 'all';
          b.classList.toggle('active', on);
          b.setAttribute('aria-pressed', on ? 'true' : 'false');
        });
        queueRender();
        return;
      }
      if (e.target.closest('[data-job-follow-btn]')) {
        following = !following;
        queueRender();
        return;
      }
      if (e.target.closest('[data-job-retry]')) {
        jobControl('restart').catch(function (err) { toast(err.message || String(err), 'danger'); });
        return;
      }
      var tg = e.target.closest('[data-run-toggle]');
      if (tg && !filtering()) {
        var key = tg.getAttribute('data-run-toggle');
        expanded[key] = tg.getAttribute('aria-expanded') !== 'true';
        renderJob();
      }
    });
    drawerEl.addEventListener('keydown', function (e) {
      var tg = e.target.closest && e.target.closest('[data-run-toggle]');
      if (tg && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); tg.click(); }
    });
    var search = jobEl('[data-job-search]');
    if (search) {
      search.addEventListener('input', function () { jobQuery = search.value; queueRender(); });
      search.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') {
          e.stopPropagation();
          e.preventDefault();
          search.value = '';
          jobQuery = '';
          search.blur();
          queueRender();
        }
      });
    }
    document.addEventListener('keydown', function (e) {
      if (e.key !== '/' || !drawerEl.classList.contains('show') || !search) { return; }
      var a = document.activeElement;
      if (a && (/^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) || a.isContentEditable)) { return; }
      e.preventDefault();
      search.focus();
    });
    var body = jobEl('[data-job-body]');
    if (body) {
      body.addEventListener('scroll', function () {
        if (!jobActive() || !following) { return; }
        if (body.scrollHeight - body.scrollTop - body.clientHeight > 24) {
          following = false;
          queueRender();
        }
      });
    }
    function bindControl(sel, op) {
      var btn = drawerEl.querySelector(sel);
      if (!btn) { return; }
      btn.addEventListener('click', function () {
        btn.disabled = true;
        jobControl(op).then(function () {
          btn.disabled = false;
        }, function (err) {
          btn.disabled = false;
          toast(err.message || String(err), 'danger');
        });
      });
    }
    bindControl('[data-job-pause]', 'pause');
    bindControl('[data-job-continue]', 'continue');
    bindControl('[data-job-abort]', 'abort');
    bindControl('[data-job-restart]', 'restart');
  }

  // --- Logs ----------------------------------------------------------------

  function startLogs() {
    var view = document.querySelector('[data-logs]');
    if (!view) { return; }
    var plain = document.getElementById('mdb-logs-plain');
    var tailSel = document.querySelector('[data-logs-tail]');
    var followBox = document.querySelector('[data-logs-follow]');
    var wrapBox = document.querySelector('[data-logs-wrap]');
    var filterInput = document.querySelector('[data-logs-filter]');
    var emptyEl = document.querySelector('[data-logs-empty]');
    var countEl = document.querySelector('[data-logs-count]');
    var pill = document.querySelector('[data-logs-pill]');
    var bottomBtn = document.querySelector('[data-logs-bottom]');
    var levelBtns = Array.prototype.slice.call(document.querySelectorAll('[data-logs-level]'));
    var dot = document.querySelector('[data-logs-dot]');
    var state = document.querySelector('[data-logs-state]');
    var logs = null;
    var max = 5000;
    var pending = 0;
    var levels = { error: true, warn: true, info: true, debug: true, other: true };

    function say(text, color, live) {
      if (state) { state.textContent = text; }
      if (dot) { dot.className = 'status-dot status-' + color + (live ? ' status-dot-animated' : ''); }
    }
    function detectLevel(raw) {
      var m = raw.match(/\b(ERROR|ERR|FATAL|SEVERE|WARN(?:ING)?|INFO|DEBUG|TRACE|NOTICE)\b/i);
      if (!m) { return 'other'; }
      var v = m[1].toUpperCase();
      if (v === 'ERROR' || v === 'ERR' || v === 'FATAL' || v === 'SEVERE') { return 'error'; }
      if (v.indexOf('WARN') === 0) { return 'warn'; }
      if (v === 'DEBUG' || v === 'TRACE') { return 'debug'; }
      if (v === 'INFO' || v === 'NOTICE') { return 'info'; }
      return 'other';
    }
    function atBottom() {
      return view.scrollTop + view.clientHeight >= view.scrollHeight - 24;
    }
    function jumpBottom() {
      view.scrollTop = view.scrollHeight;
      pending = 0;
      if (pill) { pill.classList.add('d-none'); }
    }
    function updateCount() {
      var n = view.querySelectorAll('.mdb-log-line').length;
      if (countEl) { countEl.textContent = n + (n === 1 ? ' line' : ' lines'); }
    }
    function applyFilters() {
      var q = (filterInput && filterInput.value || '').trim().toLowerCase();
      var shown = 0;
      var rows = view.querySelectorAll('.mdb-log-line');
      for (var i = 0; i < rows.length; i++) {
        var row = rows[i];
        var level = row.getAttribute('data-level') || 'other';
        var okLevel = !!levels[level];
        var okText = !q || (row.getAttribute('data-raw') || '').toLowerCase().indexOf(q) >= 0;
        var hide = !(okLevel && okText);
        row.hidden = hide;
        if (!hide) { shown++; }
        var msg = row.querySelector('.mdb-log-msg');
        if (msg) {
          var rawMsg = msg.getAttribute('data-text') || msg.textContent;
          if (!q) {
            msg.textContent = rawMsg;
          } else {
            msg.textContent = '';
            var lower = rawMsg.toLowerCase();
            var idx = 0;
            while (idx < rawMsg.length) {
              var hit = lower.indexOf(q, idx);
              if (hit < 0) {
                msg.appendChild(document.createTextNode(rawMsg.slice(idx)));
                break;
              }
              if (hit > idx) { msg.appendChild(document.createTextNode(rawMsg.slice(idx, hit))); }
              var mark = document.createElement('mark');
              mark.className = 'mdb-log-mark';
              mark.textContent = rawMsg.slice(hit, hit + q.length);
              msg.appendChild(mark);
              idx = hit + q.length;
            }
          }
        }
      }
      if (emptyEl) { emptyEl.classList.toggle('d-none', shown > 0 || rows.length === 0); }
    }
    function appendLine(raw) {
      var level = detectLevel(raw);
      var row = document.createElement('div');
      row.className = 'mdb-log-line mdb-log-' + level;
      row.setAttribute('data-level', level);
      row.setAttribute('data-raw', raw);

      var prefix = '';
      var body = raw;
      var pipe = raw.indexOf('|');
      if (pipe > 0 && pipe < 48) {
        prefix = raw.slice(0, pipe).trim();
        body = raw.slice(pipe + 1).replace(/^\s+/, '');
      }
      var prefEl = document.createElement('span');
      prefEl.className = 'mdb-log-prefix';
      prefEl.textContent = prefix || '—';
      prefEl.title = prefix || '';
      var lvlEl = document.createElement('span');
      lvlEl.className = 'mdb-log-level';
      lvlEl.textContent = level;
      var msgEl = document.createElement('span');
      msgEl.className = 'mdb-log-msg';
      msgEl.setAttribute('data-text', body);
      msgEl.textContent = body;
      row.appendChild(prefEl);
      row.appendChild(lvlEl);
      row.appendChild(msgEl);
      view.appendChild(row);

      if (plain) {
        plain.textContent = (plain.textContent ? plain.textContent + '\n' : '') + raw;
      }
      while (view.querySelectorAll('.mdb-log-line').length > max) {
        var first = view.querySelector('.mdb-log-line');
        if (first) { first.remove(); }
        if (plain) {
          var cut = plain.textContent.indexOf('\n');
          plain.textContent = cut >= 0 ? plain.textContent.slice(cut + 1) : '';
        }
      }
      applyFilters();
      updateCount();
      return row;
    }
    function connect() {
      if (logs) { logs.close(); }
      view.innerHTML = '';
      if (plain) { plain.textContent = ''; }
      pending = 0;
      if (pill) { pill.classList.add('d-none'); }
      updateCount();
      say('connecting', 'yellow', true);
      var q = '?tail=' + encodeURIComponent(tailSel.value) + '&follow=' + (followBox.checked ? '1' : '0');
      logs = new EventSource('/api/v1/services/' + encodeURIComponent(view.getAttribute('data-logs')) + '/logs' + q);
      logs.addEventListener('open', function () { say(followBox.checked ? 'following' : 'reading', 'green', followBox.checked); });
      logs.addEventListener('line', function (e) {
        var follow = atBottom() && followBox.checked;
        appendLine(JSON.parse(e.data));
        if (follow) {
          jumpBottom();
        } else if (followBox.checked) {
          pending++;
          if (pill) {
            pill.textContent = pending + (pending === 1 ? ' new line' : ' new lines');
            pill.classList.remove('d-none');
          }
        }
      });
      logs.addEventListener('end', function (e) {
        logs.close();
        var d = JSON.parse(e.data);
        if (d.error) { say('ended: ' + d.error, 'red', false); } else { say('ended', 'secondary', false); }
      });
      logs.onerror = function () {
        if (logs.readyState === EventSource.CLOSED) { say('disconnected', 'red', false); }
      };
    }
    if (wrapBox) {
      wrapBox.addEventListener('change', function () {
        view.classList.toggle('mdb-log-nowrap', !wrapBox.checked);
      });
    }
    levelBtns.forEach(function (btn) {
      btn.addEventListener('click', function () {
        var key = btn.getAttribute('data-logs-level');
        levels[key] = !levels[key];
        btn.classList.toggle('active', levels[key]);
        btn.setAttribute('aria-pressed', levels[key] ? 'true' : 'false');
        applyFilters();
      });
    });
    if (filterInput) {
      var filterTimer = null;
      filterInput.addEventListener('input', function () {
        clearTimeout(filterTimer);
        filterTimer = setTimeout(applyFilters, 80);
      });
    }
    if (bottomBtn) { bottomBtn.addEventListener('click', jumpBottom); }
    if (pill) { pill.addEventListener('click', jumpBottom); }
    view.addEventListener('scroll', function () {
      if (atBottom()) {
        pending = 0;
        if (pill) { pill.classList.add('d-none'); }
      }
    });
    tailSel.addEventListener('change', connect);
    followBox.addEventListener('change', connect);
    window.addEventListener('beforeunload', function () { if (logs) { logs.close(); } });
    connect();
  }

  // --- API tokens ----------------------------------------------------------

  function createToken() {
    openForm({
      title: 'New API token',
      command: 'dbpantry api token create NAME --scope SCOPE',
      submitLabel: 'Create',
      fields: [
        { name: 'name', label: 'Name (one word, e.g. ci)', required: true },
        { name: 'scope', label: 'Scope', required: true, options: ['read', 'operate', 'admin'], value: 'read' },
        { name: 'days', label: 'Expires in days', required: true, value: '30', type: 'number' }
      ],
      submit: function (v) {
        return api('POST', '/api/v1/tokens', { name: v.name, scope: v.scope, days: parseInt(v.days, 10) }).then(function (r) {
          var box = document.querySelector('[data-token-created]');
          if (box) {
            box.classList.remove('d-none');
            box.querySelector('#mdb-new-token').value = r.token;
          }
          toast('API token created — copy it now', 'success');
        });
      }
    });
  }

  function revokeToken(id, name) {
    openForm({
      title: 'Revoke the token ' + name,
      command: 'dbpantry api token revoke ' + id,
      danger: true,
      submitLabel: 'Revoke',
      fields: [],
      submit: function () {
        return api('DELETE', '/api/v1/tokens/' + encodeURIComponent(id)).then(function () {
          toast('Token revoked', 'success');
          refreshPage();
        });
      }
    });
  }

  // --- Webhooks ------------------------------------------------------------

  function createHook() {
    openForm({
      title: 'New webhook',
      command: 'POST /api/v1/hooks {"url": URL, "events": [...]}',
      submitLabel: 'Create',
      fields: [
        { name: 'url', label: 'URL (https; http only for localhost)', required: true },
        { name: 'events', label: 'Events, comma-separated (empty: all), e.g. job.failed,job.succeeded' }
      ],
      submit: function (v) {
        var events = (v.events || '').split(',').map(function (s) { return s.trim(); }).filter(Boolean);
        return api('POST', '/api/v1/hooks', { url: v.url, events: events }).then(function (r) {
          var box = document.querySelector('[data-hook-created]');
          if (box) {
            box.classList.remove('d-none');
            box.querySelector('#mdb-new-hook-secret').value = r.secret;
          }
          toast('Webhook created — copy its secret now', 'success');
        });
      }
    });
  }

  function deleteHook(id, url) {
    openForm({
      title: 'Delete the webhook to ' + url,
      command: 'DELETE /api/v1/hooks/' + id,
      danger: true,
      submitLabel: 'Delete',
      fields: [],
      submit: function () {
        return api('DELETE', '/api/v1/hooks/' + encodeURIComponent(id)).then(function () {
          toast('Webhook deleted', 'success');
          refreshPage();
        });
      }
    });
  }

  // --- Wiring --------------------------------------------------------------

  // Re-open a job the previous page load left running (refresh / navigation).
  try {
    var saved = JSON.parse(sessionStorage.getItem(JOB_KEY) || 'null');
    if (saved && saved.id && Date.now() - (saved.at || 0) < 6 * 60 * 60 * 1000) {
      api('GET', '/api/v1/jobs/' + encodeURIComponent(saved.id)).then(function (job) {
        if (job && job.state === 'running') {
          if (saved.title) { job.title = saved.title; }
          openJob(job.id, job);
        } else {
          clearRememberedJob();
        }
      }, function () { clearRememberedJob(); });
    }
  } catch (err) { /* ignore */ }

  window.mdbRunAction = runAction;

  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-action]');
    if (b && !b.disabled && !b.classList.contains('mdb-btn-busy')) {
      e.preventDefault();
      if (document.body.classList.contains('mdb-page-busy') && !e.target.closest('#mdb-action-modal')) {
        toast('Wait for the current action to finish preparing', 'warning');
        return;
      }
      if (window.mdbCloseMenus) { window.mdbCloseMenus(); }
      var preset = {};
      try { preset = JSON.parse(b.getAttribute('data-params') || '{}'); } catch (err) { preset = {}; }
      runAction(b.getAttribute('data-action'), preset, b);
      return;
    }
    var j = e.target.closest('[data-job-open]');
    if (j) {
      e.preventDefault();
      var m = modal();
      if (m && modalEl.classList.contains('show')) { m.hide(); }
      openJob(j.getAttribute('data-job-open'));
      return;
    }
    if (e.target.closest('[data-token-create]')) { e.preventDefault(); createToken(); return; }
    var r = e.target.closest('[data-token-revoke]');
    if (r) { e.preventDefault(); revokeToken(r.getAttribute('data-token-revoke'), r.getAttribute('data-token-name')); return; }
    if (e.target.closest('[data-hook-create]')) { e.preventDefault(); createHook(); return; }
    var hd = e.target.closest('[data-hook-delete]');
    if (hd) { e.preventDefault(); deleteHook(hd.getAttribute('data-hook-delete'), hd.getAttribute('data-hook-url')); }
  });

  startLogs();
})();
