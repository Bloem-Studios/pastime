(function () {
  var preference = 'dark';
  try { preference = localStorage.getItem('pastime-theme') || 'dark'; } catch (_) {}
  var dark = preference === 'system' ? matchMedia('(prefers-color-scheme: dark)').matches : preference !== 'light';
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  try { if (localStorage.getItem('pastime-rail') === 'true') document.documentElement.dataset.rail = 'true'; } catch (_) {}
})();
