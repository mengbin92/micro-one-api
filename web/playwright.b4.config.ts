import { defineConfig, devices } from '@playwright/test';
export default defineConfig({
  testDir: './e2e', testMatch: 'b4-operations.spec.ts', workers: 2, forbidOnly: true,
  timeout: 30_000, expect: { timeout: 10_000 }, outputDir: 'b4-test-results/artifacts',
  reporter: [['list'], ['json', { outputFile: 'b4-test-results/playwright.json' }]],
  webServer: { command: 'npm run preview -- --host 127.0.0.1 --port 4184', url: 'http://127.0.0.1:4184', reuseExistingServer: false },
  use: { baseURL: 'http://127.0.0.1:4184', trace: 'retain-on-failure' },
  projects: [{ name: 'b4-chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } }, { name: 'b4-mobile-chrome', use: { ...devices['Pixel 5'], channel: 'chrome' } }],
});
