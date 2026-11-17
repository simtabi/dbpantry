// Refreshes the page body every five seconds from /partials/body, and
// switches the colour scheme. The server renders all markup; this only
// swaps it in, so there is one template and no second renderer here.
(function () {
  var target = document.querySelector('[data-refresh-target]');
  var badge = document.querySelector('.mdb-refresh');
  var label = document.querySelector('[data-refresh-label]');
  var failures = 0;

  function setState(state, text) {
    badge.setAttribute('data-state', state);
    label.textContent = text;
  }

  function refresh() {
    if (document.hidden) { return; }
    fetch('/partials/body', { cache: 'no-store' })
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

  document.querySelector('[data-theme-toggle]').addEventListener('click', function () {
    var next = document.documentElement.getAttribute('data-bs-theme') === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-bs-theme', next);
    try { localStorage.setItem('dbpantry-theme', next); } catch (e) { /* storage blocked */ }
  });
})();
