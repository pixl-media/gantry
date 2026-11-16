import { createTheme } from '@mui/material/styles';

// Anthracite and white: the primary color is anthracite in light mode and
// white in dark mode, so buttons, tabs and focus rings carry no hue. Only the
// status colors (success, warning, error, info) are colored, because they
// carry meaning.
const anthracite = '#2b2f33';

// Light, dark or the operating system's choice, picked in the app bar. The
// mode is kept in localStorage ("mui-mode"); `public/color-scheme.js` sets
// the scheme attribute before the app renders, so there is no flash. No web
// fonts are loaded: the CSP only allows the page's own origin.
export const colorSchemeAttribute = 'data-mui-color-scheme';

export const theme = createTheme({
  cssVariables: { colorSchemeSelector: colorSchemeAttribute },
  colorSchemes: {
    light: {
      palette: {
        primary: {
          main: anthracite,
          light: '#4a5056',
          dark: '#1a1d20',
          contrastText: '#ffffff',
        },
        background: { default: '#f5f6f7', paper: '#ffffff' },
        text: { primary: '#1f2326', secondary: '#5f666d' },
      },
    },
    dark: {
      palette: {
        primary: {
          main: '#f2f3f5',
          light: '#ffffff',
          dark: '#c9cdd1',
          contrastText: '#17191c',
        },
        background: { default: '#17191c', paper: '#202326' },
        text: { primary: '#e8eaec', secondary: '#9aa1a8' },
      },
    },
  },
  typography: {
    fontFamily:
      'system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  },
  shape: { borderRadius: 8 },
  components: {
    MuiPaper: {
      styleOverrides: {
        // MUI lightens raised surfaces in dark mode with a white overlay
        // (16% for dialogs), which turns anthracite into mid grey. Dialogs
        // and menus keep the paper color; shadow and backdrop set them off.
        root: ({ theme }) =>
          theme.applyStyles('dark', { backgroundImage: 'none' }),
      },
    },
    MuiAppBar: {
      styleOverrides: {
        // In dark mode the bar has the paper color; a line sets it off from
        // the page.
        root: ({ theme }) =>
          theme.applyStyles('dark', {
            borderBottom: `1px solid ${(theme.vars ?? theme).palette.divider}`,
          }),
      },
    },
  },
});

export const monoFont =
  'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace';

/**
 * Colors of the dark console surfaces (logs, operation output, the Compose
 * editor), the same in light and dark mode.
 */
export const consoleColors = {
  background: '#1e2124',
  text: '#e6e8ea',
  muted: '#8b9298',
  hover: 'rgba(255, 255, 255, 0.04)',
};
