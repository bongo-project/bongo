// Applies the saved theme before the first paint. It is loaded without defer
// in <head> because Alpine sets data-theme only after the page has rendered,
// so a dark-mode user would otherwise see the light theme flash on every full
// page load. appState keeps the attribute up to date from then on.
(function() {
  var theme = 'auto';
  try {
    theme = localStorage.getItem('bongo-theme') || 'auto';
  } catch (e) {}
  if (theme !== 'light' && theme !== 'dark') {
    theme = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  document.documentElement.setAttribute('data-theme', theme);
})();
