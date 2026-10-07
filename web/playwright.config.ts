import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  // The live IAM suite has its own config and receives RBAC_C_FIXTURE from
  // the Go integration harness; the shared smoke gate uses mocked APIs.
  // D7 injects the shipped CSP and must run against a production build.
  testIgnore: ['rbac-permissions.spec.ts', 'playground-experience.spec.ts'],
  timeout: 30_000,
  expect: {
    timeout: 10_000,
  },
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1',
    url: 'http://127.0.0.1:5173',
    reuseExistingServer: !process.env.CI,
  },
  use: {
    baseURL: 'http://127.0.0.1:5173',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chrome',
      use: {
        ...devices['Desktop Chrome'],
        channel: 'chrome',
      },
    },
    {
      name: 'mobile-chrome',
      use: {
        ...devices['Pixel 5'],
        channel: 'chrome',
      },
    },
  ],
});
