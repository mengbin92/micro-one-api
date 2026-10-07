import { defineConfig } from '@playwright/test';
import shared from './playwright.config';

// Test the shipped bundle and lazy Markdown chunk under the existing admin CSP.
export default defineConfig({
  ...shared,
  testMatch: ['playground-experience.spec.ts', 'layout-regression.spec.ts'],
  testIgnore: 'rbac-permissions.spec.ts',
  outputDir: 'd7-test-results',
  webServer: {
    command: 'npm run preview -- --host 127.0.0.1 --port 4175',
    url: 'http://127.0.0.1:4175',
    reuseExistingServer: false,
  },
  use: { baseURL: 'http://127.0.0.1:4175', trace: 'retain-on-failure' },
});
