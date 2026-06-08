import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import { visualizer } from 'rollup-plugin-visualizer';
import path from 'path';

const BACKENDS: Record<string, string> = {
  local: 'http://localhost:8080',
};

const backendKey = process.env.BACKEND ?? 'local';
const backendTarget = BACKENDS[backendKey] ?? backendKey;

// https://vite.dev/config/
export default defineConfig({
  base: '/',
  define: {
    'import.meta.env.VITE_BASE_TITLE': JSON.stringify('clickhouse-movoor'),
    'import.meta.env.VITE_BASE_URL': JSON.stringify('http://localhost:5173'),
  },
  plugins: [
    tanstackRouter({
      routesDirectory: './src/routes',
      generatedRouteTree: './src/routeTree.gen.ts',
      autoCodeSplitting: true,
      quoteStyle: 'single',
    }),
    tailwindcss(),
    react(),
    visualizer({
      open: false,
      gzipSize: true,
      brotliSize: true,
      filename: 'build/stats.html',
    }),
  ],
  server: {
    proxy: {
      '/api': {
        target: backendTarget,
        changeOrigin: true,
        secure: backendTarget.startsWith('https'),
      },
      '/openapi.yaml': {
        target: backendTarget,
        changeOrigin: true,
        secure: backendTarget.startsWith('https'),
      },
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
    conditions: ['import', 'module', 'browser', 'default'],
  },
  build: {
    outDir: 'dist',
    commonjsOptions: {
      transformMixedEsModules: true,
    },
    rollupOptions: {
      output: {
        chunkFileNames: 'assets/[name]-[hash].js',
        entryFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash].[ext]',
      },
    },
  },
});
