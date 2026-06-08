import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'path';

/**
 * Vitest v4 config for unit tests only.
 *
 * Runs in jsdom (fast, lightweight) and excludes Storybook stories.
 * Run via: pnpm test:unit
 */
export default defineConfig({
  plugins: [react()],
  test: {
    name: 'unit',
    globals: true,
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    exclude: ['src/**/*.stories.{ts,tsx}', 'node_modules'],
    setupFiles: ['./vitest.setup.ts'],
    fakeTimers: {
      toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'setImmediate', 'clearImmediate', 'Date'],
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  optimizeDeps: {
    include: ['react-dom/client', 'zod/mini'],
  },
});
