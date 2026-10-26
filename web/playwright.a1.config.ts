import { defineConfig, devices } from '@playwright/test';

// A1 runs against the shipped bundle with local API fixtures. It does not
// send email, contact Cloudflare, or change the shared smoke suite.
export default defineConfig({
  testDir: './e2e',
  testMatch: 'a1-auth.spec.ts',
  forbidOnly: true,
  workers: 2,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  outputDir: 'a1-test-results/artifacts',
  reporter: [['list'], ['json', { outputFile: 'a1-test-results/playwright.json' }]],
  webServer: {
    command: 'npm run preview -- --host 127.0.0.1 --port 4176',
    url: 'http://127.0.0.1:4176',
    reuseExistingServer: false,
  },
  use: { baseURL: 'http://127.0.0.1:4176', trace: 'retain-on-failure' },
  projects: [
    { name: 'a1-chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } },
    { name: 'a1-mobile-chrome', use: { ...devices['Pixel 5'], channel: 'chrome' } },
  ],
});
