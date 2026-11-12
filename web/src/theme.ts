import { createTheme } from '@mui/material/styles';

// Light and dark follow the operating system through CSS media queries, so
// there is no flash on load and no JavaScript involved. No web fonts are
// loaded: the CSP only allows the page's own origin.
export const theme = createTheme({
  cssVariables: { colorSchemeSelector: 'media' },
  colorSchemes: { light: true, dark: true },
  typography: {
    fontFamily:
      'system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  },
  shape: { borderRadius: 8 },
});

export const monoFont =
  'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace';
