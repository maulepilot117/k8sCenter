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
    baseURL: process.env.BASE_URL ?? "http://localhost:5173",
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
      command: "deno task dev",
      cwd: "../frontend",
      url: "http://localhost:5173",
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
    },
  ],
});
