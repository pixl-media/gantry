import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The dev server forwards API requests to the agent, so the app and the API
// share one origin, like behind Caddy in the container. The Host header is
// kept (no changeOrigin), which the agent's same-origin check relies on.
const agent = process.env.GANTRY_AGENT_URL ?? 'http://127.0.0.1:7070';

export default defineConfig({
  plugins: [react()],
  build: {
    rolldownOptions: {
      output: {
        // The libraries in chunks of their own: each stays below the size
        // warning, loads in parallel, and stays cached across gantry
        // releases that don't update it. The Compose editor (CodeMirror)
        // is loaded lazily and keeps its own chunk.
        codeSplitting: {
          groups: [
            {
              name: 'react',
              test: /[\\/]node_modules[\\/](react|react-dom|scheduler|react-router)[\\/]/,
            },
            {
              name: 'mui',
              test: /[\\/]node_modules[\\/](@mui|@emotion|@popperjs|stylis|react-transition-group)[\\/]/,
            },
          ],
        },
      },
    },
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: agent },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
  },
});
