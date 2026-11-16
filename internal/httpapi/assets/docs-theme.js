'use strict';
// Match the app's theme before the page paints.
(function () {
  var preference = 'dark';
  try { preference = localStorage.getItem('pastime-theme') || 'dark'; } catch (_) {}
  var dark = preference === 'system' ? matchMedia('(prefers-color-scheme: dark)').matches : preference !== 'light';
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
})();
