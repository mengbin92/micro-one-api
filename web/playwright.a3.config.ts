import { defineConfig, devices } from '@playwright/test';

// UI fixtures exercise the shipped bundle; all pricing and ledger API calls
// are local fixtures and never enable production billing or contact upstreams.
export default defineConfig({
  testDir: './e2e', testMatch: 'a3-service-tier.spec.ts', workers: 2, forbidOnly: true,
  timeout: 30_000, expect: { timeout: 10_000 },
  outputDir: 'a3-test-results/artifacts',
  reporter: [['list'], ['json', { outputFile: 'a3-test-results/playwright.json' }]],
  webServer: {
    command: 'npm run preview -- --host 127.0.0.1 --port 4178',
    url: 'http://127.0.0.1:4178', reuseExistingServer: false,
  },
  use: { baseURL: 'http://127.0.0.1:4178', trace: 'retain-on-failure' },
  projects: [
    { name: 'a3-chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } },
    { name: 'a3-mobile-chrome', use: { ...devices['Pixel 5'], channel: 'chrome' } },
  ],
});
