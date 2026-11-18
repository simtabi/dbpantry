// Applies the saved or system colour scheme before the page paints, so it
// never flashes light in dark mode. Loaded in <head>, deliberately blocking.
// ?theme=dark or ?theme=light (the header's links) chooses and remembers one.
(function () {
  var key = 'dbpantry-theme';
  var asked = new URLSearchParams(window.location.search).get('theme');
  var saved = null;
  try {
    if (asked === 'dark' || asked === 'light') { localStorage.setItem(key, asked); }
    saved = localStorage.getItem(key);
  } catch (e) { saved = asked; }
  var dark = saved ? saved === 'dark' : window.matchMedia('(prefers-color-scheme: dark)').matches;
  document.documentElement.setAttribute('data-bs-theme', dark ? 'dark' : 'light');
})();
