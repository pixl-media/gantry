// Sets the color scheme before the app renders, so a light or dark mode
// picked in the app bar doesn't flash on load. It does what MUI's
// InitColorSchemeScript does, as a file because the CSP allows no inline
// scripts. Keep the key and attribute in sync with src/theme.ts.
(function () {
  var mode = 'system';
  try {
    mode = localStorage.getItem('mui-mode') || 'system';
  } catch (e) {
    // Storage blocked: follow the system.
  }
  var dark =
    mode === 'dark' ||
    (mode !== 'light' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.setAttribute(
    'data-mui-color-scheme',
    dark ? 'dark' : 'light',
  );
})();
