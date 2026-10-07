import { defineConfig, devices } from '@playwright/test';
import { resolve } from 'node:path';
const resultsDir = process.env.IAM_BROWSER_RESULT_DIR ?? resolve('iam-test-results/default');
export default defineConfig({
 testDir: './e2e', testMatch: 'rbac-permissions.spec.ts', workers: 1, timeout: 45_000,
 forbidOnly: true,
 outputDir: resolve(resultsDir, 'artifacts'),
 reporter: [['list'], ['json', { outputFile: resolve(resultsDir, 'playwright.json') }]],
 webServer: { command: 'npm run dev -- --host 127.0.0.1 --port 5174', url: 'http://127.0.0.1:5174', reuseExistingServer: false },
 use: { baseURL: 'http://127.0.0.1:5174', trace: 'retain-on-failure' },
 projects: [{ name: 'rbac-chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } }],
});
