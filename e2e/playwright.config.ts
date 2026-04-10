import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI
    ? [["github"], ["html", { open: "never" }]]
    : [["html", { open: "on-failure" }]],

  use: {
    baseURL:
      process.env.BASE_URL ??
      (process.env.CI ? "http://localhost:8000" : "http://localhost:5173"),
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },

  // Backend must start first — frontend BFF proxy depends on it.
  // Playwright starts webServer entries sequentially in array order.
  // In CI, E2E_BACKEND_COMMAND uses the pre-built binary to avoid compile time.
  webServer: [
    {
      command:
        process.env.E2E_BACKEND_COMMAND ??
        'go run ./cmd/kubecenter --config ""',
      cwd: "../backend",
      url: "http://localhost:8080/healthz",
      timeout: 120_000,
      reuseExistingServer: !process.env.CI,
      env: {
        ...process.env,
        KUBECENTER_DEV: "true",
        KUBECENTER_AUTH_JWTSECRET:
          "e2e-test-secret-minimum-32-bytes-long!!",
        KUBECENTER_AUTH_SETUPTOKEN: "e2e-setup-token",
        KUBECENTER_DATABASE_URL:
          process.env.KUBECENTER_DATABASE_URL ??
          "postgresql://k8scenter:k8scenter@localhost:5432/k8scenter?sslmode=disable",
      },
    },
    {
      // In CI, use the pre-built production server (more stable than Vite dev server).
      // Locally, use Vite dev server for HMR convenience.
      command: process.env.CI ? "deno task start" : "deno task dev",
      cwd: "../frontend",
      url: process.env.CI ? "http://localhost:8000" : "http://localhost:5173",
      timeout: 120_000,
      reuseExistingServer: !process.env.CI,
    },
  ],

  projects: [
    { name: "setup", testDir: "./fixtures", testMatch: /.*\.setup\.ts/ },
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        storageState: "playwright/.auth/admin.json",
      },
      dependencies: ["setup"],
      // api-routes.spec.ts runs as its own project so its ~100 tests don't
      // share the runtime budget with the main smoke suite (wizard-flows is
      // timing-sensitive and occasionally flakes under added load).
      testIgnore: /api-routes\.spec\.ts/,
    },
    {
      name: "route-contract",
      testMatch: /api-routes\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: "playwright/.auth/admin.json",
      },
      // Run strictly after the main suite finishes so its tests can't
      // compete for backend rate-limit buckets or browser resources.
      dependencies: ["chromium"],
    },
  ],
});
