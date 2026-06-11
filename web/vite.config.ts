/// <reference types="vitest" />
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import path from 'path';

const vendorChunks = [
  {
    name: 'charts',
    test: /node_modules[\\/](?:recharts|d3-|decimal\.js-light|es-toolkit|victory-vendor)[\\/]/,
    priority: 40,
  },
  {
    name: 'react',
    test: /node_modules[\\/](?:react|react-dom|react-is|react-router|react-router-dom|scheduler)[\\/]/,
    priority: 30,
  },
  {
    name: 'query',
    test: /node_modules[\\/](?:@tanstack[\\/]react-query|@tanstack[\\/]query-core|axios)[\\/]/,
    priority: 20,
  },
  {
    name: 'ui',
    test: /node_modules[\\/](?:@base-ui|lucide-react|sonner|class-variance-authority|clsx|tailwind-merge)[\\/]/,
    priority: 10,
  },
  {
    name: 'vendor',
    test: /node_modules[\\/]/,
    priority: 0,
  },
];

export default defineConfig({
  plugins: [tailwindcss(), react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          minSize: 20 * 1024,
          groups: vendorChunks,
        },
      },
    },
  },
  test: {
    environment: 'jsdom',
    exclude: ['e2e/**', 'node_modules/**', 'dist/**'],
    setupFiles: './src/test/setup.ts',
    globals: true,
  },
});
