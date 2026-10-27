import { defineConfig, devices } from '@playwright/test';

// SingleScore end-to-end tests.
//
// These tests run against a locally-running backend + frontend. The
// `webServer` block starts the frontend (webpack-dev-server), which in
// turn proxies /graphql to the backend. The backend must already be
// running (see README: "Running the stack").
export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL || 'http://127.0.0.1:3000',
    actionTimeout: 10_000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
