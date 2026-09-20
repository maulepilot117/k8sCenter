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
      // In CI, use the pre-built production server (more stable than the Vite
      // dev server). Locally, use `astro dev` for HMR convenience.
      //
      // Both sides run frontend/server/*: `bun run start` is server/prod.ts,
      // and `astro dev` mounts the same dispatch through server/dev-plugin.ts.
      // That shared entrypoint is what R18 is about -- the two must not
      // diverge on /ws, security headers, or the /api BFF proxy again.
      command: process.env.CI ? "bun run start" : "bun run dev",
      cwd: "../frontend",
      url: process.env.CI ? "http://localhost:8000" : "http://localhost:5173",
      timeout: 120_000,
      reuseExistingServer: !process.env.CI,
    },
    // CI only: a second frontend, running `astro dev` beside the built one.
    //
    // This exists for websocket-rejection.spec.ts. R18's whole claim is that
    // dev and prod share one WebSocket implementation and cannot drift apart
    // on the guards -- and the only way to test that is to have both running
    // and assert they answer identically. Locally the configured server IS
    // the dev server, so the comparison would be against itself; the spec
    // falls back to a source-level assertion there and says so.
    //
    // Playwright owns the lifecycle (readiness probe, teardown) rather than a
    // backgrounded shell step in the workflow, which is what makes this safe
    // to add to a timing-sensitive suite. E2E_ALT_BASE_URL in e2e.yml is what
    // points the spec at it.
    ...(process.env.CI
      ? [
          {
            command: "bun run dev",
            cwd: "../frontend",
            url: "http://localhost:5173",
            timeout: 120_000,
            reuseExistingServer: false,
          },
        ]
      : []),
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
      //
      // discoverability.spec.ts is excluded for a different reason: it deletes
      // every pin and saved view the shared admin user owns, and pins.spec.ts
      // and saved-views.spec.ts (both in this project) own those same records.
      //
      // dashboard-layout.spec.ts is excluded for that same reason, one record
      // over: it really saves dashboard layouts, and every other dashboard
      // spec in this project renders the shipped default and asserts where it
      // puts things.
      testIgnore: [
        /api-routes\.spec\.ts/,
        /dashboard-layout\.spec\.ts/,
        /discoverability\.spec\.ts/,
        /route-inventory\.spec\.ts/,
      ],
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
    {
      // route-inventory walks every page under frontend/src/pages and asserts
      // one HTTP outcome each -- ~200 tests. They use only the `request`
      // fixture, so they are cheap, but that is exactly why api-routes.spec.ts
      // was split out too: wizard-flows is timing-sensitive and should not
      // share its runtime budget with a couple of hundred siblings.
      name: "route-inventory",
      testMatch: /route-inventory\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: "playwright/.auth/admin.json",
      },
      dependencies: ["chromium"],
    },
    {
      // The dashboard builder's acceptance specs, and the one dashboard file
      // that uses the REAL /preferences/layouts endpoint rather than the stub
      // in tests/dashboard-layout-stub.ts.
      //
      // Same isolation discoverability gets, and for the same reason: a layout
      // is stored per (user, cluster, scope), the whole suite shares one login,
      // and dashboard-grid/dashboard/dashboard-edit all render the shipped
      // default and assert where it puts things. fullyParallel:false orders
      // tests WITHIN a file, not across files, and workers is pinned to 1 only
      // in CI -- so without its own project this file could land in a worker
      // beside them locally and change what they see mid-test.
      name: "dashboard-layout",
      testMatch: /dashboard-layout\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: "playwright/.auth/admin.json",
      },
      dependencies: ["chromium"],
    },
    {
      name: "discoverability",
      testMatch: /discoverability\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: "playwright/.auth/admin.json",
      },
      // Runs strictly after the main suite because its specs call
      // deleteAllPins / deleteAllSavedViews, which wipe every preference record
      // the shared admin user owns -- the same records pins.spec.ts and
      // saved-views.spec.ts create and assert on.
      //
      // fullyParallel:false is not enough on its own: it orders tests WITHIN a
      // file, not across files, and workers is only pinned to 1 in CI. Locally
      // these three files could land in different workers, and the delete would
      // race a sibling mid-test. Safe beside route-contract, which touches no
      // preference routes.
      dependencies: ["chromium"],
    },
  ],
});
