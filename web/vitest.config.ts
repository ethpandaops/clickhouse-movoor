import { defineConfig } from 'vitest/config';
import { storybookTest } from '@storybook/addon-vitest/vitest-plugin';
import { playwright } from '@vitest/browser-playwright';
import react from '@vitejs/plugin-react';
import path from 'path';

/**
 * Vitest v4 config for Storybook tests.
 *
 * The storybookTest plugin MUST be at the root plugins level, and when used,
 * it affects the entire config. Therefore, we need separate configs for unit
 * and storybook tests in Vitest v4.
 *
 * Run via: pnpm test:storybook or pnpm test (default)
 * For unit tests, see vitest.config.unit.ts
 */
export default defineConfig({
  plugins: [
    react(),
    storybookTest({
      configDir: path.join(__dirname, '.storybook'),
      tags: {
        exclude: ['test-exclude'],
      },
    }),
  ],
  test: {
    name: 'storybook',
    globals: true,
    environment: 'jsdom',
    browser: {
      enabled: true,
      headless: true,
      provider: playwright(),
      instances: [
        {
          browser: 'chromium',
        },
      ],
    },
    setupFiles: ['./.storybook/vitest-setup.ts'],
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
    include: [
      'react',
      'react-dom',
      'react-dom/client',
      'react/jsx-dev-runtime',
      'react/jsx-runtime',
      '@heroicons/react/24/solid',
      '@tanstack/react-query',
      '@tanstack/react-router',
      'clsx',
      'culori',
      'zod',
      'zod/mini',
      '@testing-library/jest-dom',
      '@storybook/addon-docs',
      '@storybook/addon-docs/blocks',
      'msw-storybook-addon',
      'storybook/internal/channels',
      'storybook/internal/client-logger',
      'storybook/internal/docs-tools',
      'storybook/internal/preview-api',
      'storybook/internal/preview/runtime',
      'storybook/preview-api',
      'storybook/test',
      'storybook/viewport',
    ],
  },
});
