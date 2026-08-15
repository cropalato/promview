import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The dev server proxies API calls to the Go backend so the browser app can
// use relative, same-origin URLs in every environment (embedded, dev, Tauri).
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
  },
});
