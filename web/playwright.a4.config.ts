import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e', testMatch: 'a4-payment.spec.ts', workers: 2, forbidOnly: true,
  timeout: 30_000, expect: { timeout: 10_000 },
  outputDir: 'a4-test-results/artifacts',
  reporter: [['list'], ['json', { outputFile: 'a4-test-results/playwright.json' }]],
  webServer: {
    command: 'npm run preview -- --host 127.0.0.1 --port 4179',
    url: 'http://127.0.0.1:4179', reuseExistingServer: false,
  },
  use: { baseURL: 'http://127.0.0.1:4179', trace: 'retain-on-failure' },
  projects: [
    { name: 'a4-chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } },
    { name: 'a4-mobile-chrome', use: { ...devices['Pixel 5'], channel: 'chrome' } },
  ],
});
