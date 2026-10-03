import { defineConfig, devices } from '@playwright/test';
export default defineConfig({
 testDir: './e2e', testMatch: 'rbac-permissions.spec.ts', workers: 1, timeout: 45_000,
 reporter: [['list']],
 webServer: { command: 'npm run dev -- --host 127.0.0.1 --port 5174', url: 'http://127.0.0.1:5174', reuseExistingServer: false },
 use: { baseURL: 'http://127.0.0.1:5174', trace: 'retain-on-failure' },
 projects: [{ name: 'rbac-chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } }],
});
