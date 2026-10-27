import { defineConfig, devices } from '@playwright/test';

if (!process.env.A2_BASE_URL) throw new Error('Run through A2_PLAYWRIGHT=1 go test ./app/identity/cmd/identity -run TestSecurityOwnerIntegration');

export default defineConfig({
  testDir: './e2e', testMatch: 'a2-security.spec.ts', workers: 1,
  timeout: 60_000, expect: { timeout: 10_000 },
  outputDir: 'a2-test-results/artifacts', reporter: [['list']],
  // Authentication traces would contain test secrets and one-time codes.
  use: { baseURL: process.env.A2_BASE_URL, trace: 'off', screenshot: 'off', video: 'off' },
  projects: [
    { name: 'a2-chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } },
    { name: 'a2-mobile-chrome', use: { ...devices['Pixel 5'], channel: 'chrome' } },
  ],
});
