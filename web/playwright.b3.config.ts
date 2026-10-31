import { defineConfig, devices } from '@playwright/test';
export default defineConfig({
  testDir: './e2e', testMatch: 'b3-backup.spec.ts', workers: 2, forbidOnly: true,
  timeout: 30_000, expect: { timeout: 10_000 }, outputDir: 'b3-test-results/artifacts',
  reporter: [['list'], ['json', { outputFile: 'b3-test-results/playwright.json' }]],
  webServer: { command: 'npm run preview -- --host 127.0.0.1 --port 4183', url: 'http://127.0.0.1:4183', reuseExistingServer: false },
  use: { baseURL: 'http://127.0.0.1:4183', trace: 'retain-on-failure' },
  projects: [{ name: 'b3-chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } }, { name: 'b3-mobile-chrome', use: { ...devices['Pixel 5'], channel: 'chrome' } }],
});
