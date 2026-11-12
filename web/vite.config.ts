import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The dev server forwards API requests to the agent, so the app and the API
// share one origin, like behind Caddy in the container. The Host header is
// kept (no changeOrigin), which the agent's same-origin check relies on.
const agent = process.env.GANTRY_AGENT_URL ?? 'http://127.0.0.1:7070';

export default defineConfig({
  plugins: [react()],
  build: {
    // One bundle of ~220 KB gzip (mostly MUI). It's an admin UI that is
    // loaded once and then cached as immutable, so splitting isn't worth it.
    chunkSizeWarningLimit: 800,
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
