// Applies the saved or system colour scheme before the page paints, so it
// never flashes light in dark mode. Loaded in <head>, deliberately blocking.
(function () {
  var saved = null;
  try { saved = localStorage.getItem('dbpantry-theme'); } catch (e) { /* storage blocked */ }
  var dark = saved ? saved === 'dark' : window.matchMedia('(prefers-color-scheme: dark)').matches;
  document.documentElement.setAttribute('data-bs-theme', dark ? 'dark' : 'light');
})();
