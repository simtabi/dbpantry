// The dashboard's behaviour: refresh the page's content every five seconds
// from the server (one template renders it, so there is no second renderer
// here), switch the colour scheme without a reload, reveal a blurred
// password, and copy a block. A page marked data-no-refresh (a project's
// page, which holds a password) is never refreshed.
(function () {
  var target = document.querySelector('[data-refresh-target]');
  var badge = document.querySelector('.mdb-refresh');
  var label = document.querySelector('[data-refresh-label]');
  var failures = 0;

  function setState(state, text) {
    if (!badge) { return; }
    badge.setAttribute('data-state', state);
    label.textContent = text;
  }

  function refresh() {
    if (document.hidden || target.hasAttribute('data-no-refresh')) { return; }
    fetch(window.location.pathname, { cache: 'no-store', headers: { 'X-Partial': '1' } })
      .then(function (r) {
        if (!r.ok) { throw new Error('HTTP ' + r.status); }
        return r.text();
      })
      .then(function (html) {
        target.innerHTML = html;
        failures = 0;
        setState('live', 'Live');
      })
      .catch(function () {
        failures += 1;
        setState(failures > 2 ? 'error' : 'stale', failures > 2 ? 'Disconnected' : 'Retrying');
      });
  }

  setInterval(refresh, 5000);
  document.addEventListener('visibilitychange', refresh);

  document.addEventListener('click', function (e) {
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
      var from = document.querySelector(copy.getAttribute('data-copy'));
      var text = from ? from.textContent : '';
      var done = copy.querySelector('[data-copy-label]');
      var was = done ? done.textContent : '';
      navigator.clipboard.writeText(text).then(function () {
        if (done) { done.textContent = 'Copied'; setTimeout(function () { done.textContent = was; }, 1500); }
      });
    }
  });
})();
