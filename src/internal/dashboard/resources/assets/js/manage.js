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

  var choiceKinds = { engine: true, service: true, volume: true, dump: true, 'env-type': true, 'env-name': true };
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
        setPageBusy(true, 'Starting ' + a.title + '…');
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

  function badge(state, code) {
    var b = el('span', { 'class': 'badge' });
    if (state === 'running') { b.className += ' bg-yellow-lt'; b.textContent = 'running'; }
    else if (state === 'paused') { b.className += ' bg-azure-lt'; b.textContent = 'paused'; }
    else if (state === 'succeeded') { b.className += ' bg-green-lt'; b.textContent = 'succeeded'; }
    else if (state === 'cancelled') { b.className += ' bg-secondary-lt'; b.textContent = 'aborted'; }
    else { b.className += ' bg-red-lt'; b.textContent = 'failed, exit ' + code; }
    return b;
  }

  var currentJobId = null;
  var currentJobAction = null;

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
    show('[data-job-restart]', !active && !!currentJobId);
    var lockedText = drawerEl.querySelector('[data-job-locked-text]');
    if (lockedText) {
      lockedText.textContent = state === 'paused'
        ? 'Paused — continue or abort'
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

  function seconds(from, to) {
    var s = Math.max(0, Math.round(((to ? new Date(to) : new Date()) - new Date(from)) / 1000));
    return s < 60 ? s + ' s' : Math.floor(s / 60) + ' min ' + (s % 60) + ' s';
  }

  function followOn() {
    var box = drawerEl && drawerEl.querySelector('[data-job-follow]');
    return !box || box.checked;
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

  var jobPlain = null;
  var jobActiveStep = null;

  function progressWrap() {
    return drawerEl && drawerEl.querySelector('[data-job-progress-wrap]');
  }

  function setProgress(pct, eta, stepLabel) {
    var wrap = progressWrap();
    var bar = drawerEl && drawerEl.querySelector('[data-job-progress]');
    if (!wrap || !bar) { return; }
    wrap.classList.remove('d-none');
    var span = bar.querySelector('span');
    var pctEl = drawerEl.querySelector('[data-job-pct]');
    var etaEl = drawerEl.querySelector('[data-job-eta]');
    var stepEl = drawerEl.querySelector('[data-job-step]');
    if (stepLabel && stepEl) { stepEl.textContent = stepLabel; }
    if (pct == null || pct < 0) {
      bar.classList.remove('mdb-job-progress-known');
      if (span) { span.style.width = ''; }
      bar.removeAttribute('aria-valuenow');
      if (pctEl) { pctEl.textContent = ''; }
      // Keep ETA / elapsed text while the indeterminate bar animates.
      if (etaEl) { etaEl.textContent = eta || ''; }
      return;
    }
    var n = Math.min(100, Math.round(pct));
    bar.classList.add('mdb-job-progress-known');
    if (span) { span.style.width = n + '%'; }
    bar.setAttribute('aria-valuenow', String(n));
    if (pctEl) { pctEl.textContent = n + '%'; }
    if (etaEl) { etaEl.textContent = eta || ''; }
  }

  // durationSeconds parses pretty Duration tails (850ms, 4.2s, 25s, 1m05s).
  function durationSeconds(meta) {
    var s = String(meta || '').trim();
    if (!s) { return 0; }
    var m;
    if ((m = /^(\d+(?:\.\d+)?)ms$/.exec(s))) { return 0; }
    if ((m = /^(\d+(?:\.\d+)?)s$/.exec(s))) { return parseFloat(m[1]); }
    if ((m = /^(\d+)m(\d{2})s$/.exec(s))) { return parseInt(m[1], 10) * 60 + parseInt(m[2], 10); }
    if ((m = /^(\d+)h(\d{2})m$/.exec(s))) { return parseInt(m[1], 10) * 3600 + parseInt(m[2], 10) * 60; }
    return 0;
  }

  // treeResourceKey turns "├─ ✔ mdblive-pg-1" into a stable coalesce id.
  function treeResourceKey(plain) {
    var body = String(plain || '').replace(/^[└├│─\s]+/, '').replace(/^[✔✖⚠○…▸–\s]+/, '').trim();
    if (!body) { return ''; }
    var cut = body.indexOf('  ');
    if (cut > 0) { body = body.slice(0, cut); }
    return 'tree:' + body.replace(/\s+$/, '');
  }

  // classifyJobLine turns a CLI/pretty stream line into a row kind the
  // drawer can colour and coalesce (spinner frames → one updating step).
  function classifyJobLine(raw) {
    var line = String(raw || '').replace(/\s+$/, '');
    var plain = line.replace(/\u001b\[[0-9;]*[A-Za-z]/g, '');
    if (!plain) { return null; }
    var m;
    if (/^dbpantry\s*[›>]/.test(plain) || (/^dbpantry\s/.test(plain) && plain.indexOf('·') >= 0)) {
      return { kind: 'title', text: plain.replace(/^dbpantry\s*[›>]?\s*/, 'dbpantry › ') };
    }
    // Verify harness: → 01-name  description
    m = /^→\s+(\S+)\s{2,}(.*)$/.exec(plain) || /^→\s+(\S+)\s+(.*)$/.exec(plain);
    if (m) {
      return { kind: 'title', text: m[1], meta: (m[2] || '').trim(), step: true, coalesceKey: 'check:' + m[1] };
    }
    m = /^\s{2}✓\s+pass\b(.*)$/.exec(plain);
    if (m) { return { kind: 'done', text: 'pass', meta: (m[1] || '').trim() }; }
    m = /^\s{2}✗\s+fail\b(.*)$/.exec(plain);
    if (m) { return { kind: 'fail', text: 'fail', meta: (m[1] || '').trim() }; }
    m = /^\s{2}○\s+skip\b(.*)$/.exec(plain);
    if (m) { return { kind: 'plain', text: 'skip', meta: (m[1] || '').trim() }; }
    if (/[█░]/.test(plain) && /%\b/.test(plain)) {
      var p2 = /(\d{1,3})%\b/.exec(plain);
      var e2 = /~([^\s]+)\s+left/.exec(plain) || /(estimating…)/.exec(plain);
      return {
        kind: 'progress',
        text: plain.trim(),
        pct: p2 ? parseInt(p2[1], 10) : null,
        eta: e2 ? e2[0] : '',
        coalesceKey: 'progress:' + plain.trim().replace(/\d+%?/g, '#')
      };
    }
    if (/^[└├│─\s]+/.test(plain) && /[└├│]/.test(plain)) {
      var treeBody = plain.replace(/^[└├│─\s]+/, '');
      var treeKind = 'tree';
      if (/^✖/.test(treeBody)) { treeKind = 'fail'; }
      else if (/^⚠/.test(treeBody)) { treeKind = 'warn'; }
      else if (/^✔/.test(treeBody) || /\bDone\b/i.test(treeBody)) { treeKind = 'done'; }
      else if (/^[…▸⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]/.test(treeBody)) { treeKind = 'running'; }
      var treeMeta = '';
      var treePct = null;
      var treeEta = '';
      var tm = /\s{2,}(.+)$/.exec(treeBody);
      if (tm) {
        treeMeta = tm[1].trim();
        var tpm = /(\d{1,3})%\b/.exec(treeMeta);
        if (tpm) { treePct = parseInt(tpm[1], 10); }
        var tem = /~([^\s]+)\s+left/.exec(treeMeta) || /(estimating…)/.exec(treeMeta);
        if (tem) { treeEta = tem[0]; }
      }
      return {
        kind: treeKind,
        text: plain,
        meta: treePct == null ? treeMeta : '',
        pct: treePct,
        eta: treeEta,
        indent: true,
        coalesceKey: treeResourceKey(plain)
      };
    }
    if (/^\s{6}\S/.test(plain) || /^\s{4,}\S/.test(plain)) {
      var detail = plain.replace(/^\s+/, '');
      var dk = 'tree';
      if (/^FAIL:/.test(detail) || /^✖/.test(detail)) { dk = 'fail'; }
      else if (/^⚠|SC\d{4}/.test(detail)) { dk = 'warn'; }
      else if (/^✓|pass\b/i.test(detail)) { dk = 'done'; }
      return { kind: dk, text: detail, indent: true, coalesceKey: treeResourceKey(detail) };
    }
    m = /^([✔✖⚠○…▸]|[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏])\s+(.+?)(?:\s{2,}(.+))?$/.exec(plain);
    if (m) {
      var icon = m[1];
      var label = m[2].trim();
      var rest = (m[3] || '').trim();
      var kind = 'running';
      if (icon === '✔') { kind = 'done'; }
      else if (icon === '✖') { kind = 'fail'; }
      else if (icon === '⚠') { kind = 'warn'; }
      else if (icon === '○' || icon === '–') { kind = 'plain'; }
      var pct = null;
      var eta = '';
      var pm = /(\d{1,3})%\b/.exec(rest);
      if (pm) { pct = parseInt(pm[1], 10); }
      var em = /~([^\s]+)\s+left/.exec(rest) || /(estimating…)/.exec(rest);
      if (em) { eta = em[0]; }
      var dur = '';
      var dm = /\b(\d+(?:\.\d+)?(?:ms|s)|\d+m\d{2}s|\d+h\d{2}m)\b/.exec(rest);
      if (dm && pct == null) { dur = dm[1]; }
      return { kind: kind, text: label, meta: dur || rest, pct: pct, eta: eta, step: true };
    }
    m = /^(Container|Service|Network|Volume)\s+(\S+)\s+(Restarting|Starting|Stopping|Started|Stopped|Created|Removed|Healthy|Running|Error|Failed)\b(.*)$/i.exec(plain);
    if (m) {
      var action = m[3].toLowerCase();
      var ck = /^(started|stopped|created|removed|healthy|running)$/.test(action) ? 'done'
        : /^(error|failed)$/.test(action) ? 'fail'
        : 'running';
      return { kind: ck, text: m[1] + ' ' + m[2], meta: m[3] + (m[4] || ''), step: true, coalesceKey: m[1] + ' ' + m[2] };
    }
    if (/^summary\b/i.test(plain) || /^\s*\d+\s+passed/.test(plain)) {
      return { kind: 'title', text: plain.trim() };
    }
    if (/\b(error|failed|fatal)\b/i.test(plain)) {
      return { kind: 'fail', text: plain };
    }
    if (/\b(warn(ing)?)\b/i.test(plain) || /^⚠/.test(plain)) {
      return { kind: 'warn', text: plain };
    }
    return { kind: 'plain', text: plain };
  }

  function markFor(kind) {
    if (kind === 'done') { return '✔'; }
    if (kind === 'fail') { return '✖'; }
    if (kind === 'warn') { return '⚠'; }
    if (kind === 'title') { return '›'; }
    // Tree lines already carry ├─ / └─ / ✔ in the text; a mark column of
    // "·" was the dotted left gutter in the job drawer (design.md D-105).
    if (kind === 'tree' || kind === 'plain' || kind === 'progress' || kind === 'running') {
      return '';
    }
    return '';
  }

  function findJobRow(out, key) {
    if (!key || !out) { return null; }
    if (jobActiveStep && jobActiveStep.getAttribute('data-job-key') === key) {
      return jobActiveStep;
    }
    return out.querySelector('.mdb-job-line[data-job-key="' + key.replace(/"/g, '') + '"]');
  }

  function setRowProgress(row, pct, longRunning) {
    if (!row) { return; }
    var bar = row.querySelector('.mdb-job-row-bar');
    if (pct == null && !longRunning) {
      if (bar) { bar.remove(); }
      return;
    }
    if (!bar) {
      bar = el('div', { 'class': 'mdb-job-row-bar', role: 'progressbar', 'aria-valuemin': '0', 'aria-valuemax': '100' });
      bar.appendChild(el('span'));
      row.appendChild(bar);
    }
    var fill = bar.querySelector('span');
    if (pct == null) {
      bar.classList.remove('is-known');
      bar.removeAttribute('aria-valuenow');
      if (fill) { fill.style.width = ''; }
      return;
    }
    var n = Math.min(100, Math.round(pct));
    bar.classList.add('is-known');
    bar.setAttribute('aria-valuenow', String(n));
    if (fill) { fill.style.width = n + '%'; }
  }

  function appendJobLine(parsed) {
    var out = drawerEl.querySelector('#mdb-job-output');
    if (!out || !parsed) { return; }
    var key = parsed.coalesceKey || (parsed.step ? parsed.text : null);
    var row = findJobRow(out, key);
    if (!row) {
      row = el('div', { 'class': 'mdb-job-line' });
      row.appendChild(el('span', { 'class': 'mdb-job-mark' }));
      row.appendChild(el('span', { 'class': 'mdb-job-msg' }));
      row.appendChild(el('span', { 'class': 'mdb-job-meta' }));
      out.appendChild(row);
      if (key) {
        row.setAttribute('data-job-key', key);
        if (parsed.step || parsed.kind === 'running') { jobActiveStep = row; }
      }
    }
    row.className = 'mdb-job-line mdb-job-' + parsed.kind + (parsed.indent ? ' mdb-job-indent' : '');
    var mark = row.querySelector('.mdb-job-mark');
    var msg = row.querySelector('.mdb-job-msg');
    var meta = row.querySelector('.mdb-job-meta');
    mark.textContent = '';
    if (parsed.kind === 'running') {
      mark.appendChild(el('span', { 'class': 'mdb-job-spin', role: 'status', 'aria-label': 'running' }));
    } else {
      mark.textContent = markFor(parsed.kind);
    }
    msg.textContent = parsed.text || '';
    meta.textContent = parsed.meta || '';
    updateJobLineCount();
    if (parsed.kind === 'done' || parsed.kind === 'fail') {
      if (jobActiveStep === row) { jobActiveStep = null; }
      setRowProgress(row, null, false);
    }
    var longRunning = parsed.kind === 'running' && durationSeconds(parsed.meta) >= 3;
    setRowProgress(row, parsed.pct, longRunning);
    if (parsed.pct != null) {
      setProgress(parsed.pct, parsed.eta, parsed.text);
    } else if (parsed.kind === 'running' && parsed.step) {
      var stepEl = drawerEl.querySelector('[data-job-step]');
      if (stepEl) { stepEl.textContent = parsed.text; }
      // After 3s without a compose %, show the header bar as indeterminate.
      if (longRunning) { setProgress(null, parsed.meta ? ('elapsed ' + parsed.meta) : '', parsed.text); }
    } else if (parsed.kind === 'progress' && parsed.pct != null) {
      setProgress(parsed.pct, parsed.eta, drawerEl.querySelector('[data-job-step]') && drawerEl.querySelector('[data-job-step]').textContent);
    }
  }

  function updateJobLineCount() {
    var eln = drawerEl && drawerEl.querySelector('[data-job-lines]');
    var out = drawerEl && drawerEl.querySelector('#mdb-job-output');
    if (!eln || !out) { return; }
    var n = out.querySelectorAll('.mdb-job-line').length;
    eln.textContent = n + (n === 1 ? ' line' : ' lines');
  }

  function resetJobOutput() {
    var out = drawerEl.querySelector('#mdb-job-output');
    jobPlain = drawerEl.querySelector('#mdb-job-plain');
    jobActiveStep = null;
    if (out) { out.textContent = ''; }
    if (jobPlain) { jobPlain.textContent = ''; }
    updateJobLineCount();
  }

  function setJob(job) {
    currentJobId = job.id || currentJobId;
    currentJobAction = job.action || currentJobAction;
    drawerEl.querySelector('[data-job-title]').textContent = jobTitle(job);
    drawerEl.querySelector('[data-job-command]').textContent = 'dbpantry ' + (job.args || []).join(' ') + (job.actor ? '  ·  ' + job.actor : '');
    var st = drawerEl.querySelector('[data-job-state]');
    st.textContent = '';
    st.appendChild(badge(job.state, job.exit_code));
    var wrap = progressWrap();
    if (wrap) {
      wrap.classList.remove('d-none', 'is-done', 'is-fail');
      if (job.state === 'succeeded') { wrap.classList.add('is-done'); setProgress(100, '', 'Finished'); }
      else if (job.state === 'failed' || job.state === 'cancelled') { wrap.classList.add('is-fail'); setProgress(100, '', job.state === 'cancelled' ? 'Aborted' : 'Failed'); }
      else if (job.state === 'paused') { setProgress(null, '', 'Paused'); }
      else { setProgress(null); }
      if (job.state !== 'running' && job.state !== 'paused' && job.state !== 'succeeded' && job.state !== 'failed' && job.state !== 'cancelled') {
        wrap.classList.add('d-none');
      }
    }
    setDrawerLocked(job.state === 'running' || job.state === 'paused');
    setJobControls(job.state);
    if (job.state === 'running' || job.state === 'paused') { rememberJob(job.id, job); }
    else { clearRememberedJob(); }
    clearInterval(ticker);
    var elapsed = drawerEl.querySelector('[data-job-elapsed]');
    elapsed.textContent = seconds(job.started, job.ended);
    if (job.state === 'running' || job.state === 'paused') {
      ticker = setInterval(function () { elapsed.textContent = seconds(job.started); }, 1000);
    }
  }

  function openJob(id, known) {
    if (!drawerEl) { return; }
    if (stream) { stream.close(); stream = null; }
    var out = drawerEl.querySelector('#mdb-job-output');
    var empty = drawerEl.querySelector('[data-job-empty]');
    resetJobOutput();
    if (empty) { empty.classList.remove('d-none'); }
    if (known) { setJob(known); }
    else {
      drawerEl.querySelector('[data-job-title]').textContent = 'Job';
      drawerEl.querySelector('[data-job-command]').textContent = '';
      drawerEl.querySelector('[data-job-state]').textContent = '';
      drawerEl.querySelector('[data-job-elapsed]').textContent = '';
      var wrap = progressWrap();
      if (wrap) { wrap.classList.remove('d-none', 'is-done', 'is-fail'); }
      setDrawerLocked(true);
      setProgress(null);
    }
    if (tabler.Offcanvas) { tabler.Offcanvas.getOrCreateInstance(drawerEl).show(); }
    stream = new EventSource('/api/v1/jobs/' + encodeURIComponent(id) + '/events');
    stream.addEventListener('line', function (e) {
      if (empty) { empty.classList.add('d-none'); }
      var line = JSON.parse(e.data);
      if (jobPlain) { jobPlain.textContent += line + '\n'; }
      appendJobLine(classifyJobLine(line));
      if (followOn() && out) { out.scrollTop = out.scrollHeight; }
    });
    stream.addEventListener('end', function (e) {
      stream.close();
      stream = null;
      var job = JSON.parse(e.data);
      if (known && known.title) { job.title = known.title; }
      setJob(job);
      if (empty) { empty.classList.add('d-none'); }
      var title = jobTitle(job);
      if (job.state === 'succeeded') { toast(title + ' finished', 'success'); }
      else { toast(title + ' failed' + (job.exit_code ? ' (exit ' + job.exit_code + ')' : ''), 'danger'); }
      refreshPage();
    });
    stream.onerror = function () {
      if (stream && stream.readyState === EventSource.CLOSED) { stream = null; }
    };
    if (!known) {
      api('GET', '/api/v1/jobs/' + encodeURIComponent(id)).then(function (job) {
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
    var scrollBtn = drawerEl.querySelector('[data-job-scroll]');
    if (scrollBtn) {
      scrollBtn.addEventListener('click', function () {
        var view = drawerEl.querySelector('#mdb-job-output');
        if (view) { view.scrollTop = view.scrollHeight; }
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
    if (r) { e.preventDefault(); revokeToken(r.getAttribute('data-token-revoke'), r.getAttribute('data-token-name')); }
  });

  startLogs();
})();
