import { test as setup, expect } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const authFile = path.join(__dirname, "../playwright/.auth/admin.json");

setup("create admin and authenticate", async ({ page, request }) => {
  // Idempotent setup — ignore 409 if already initialized
  await request.post("/api/v1/setup/init", {
    data: {
      username: "admin",
      password: "admin123",
      setupToken: "e2e-setup-token",
    },
    headers: { "X-Requested-With": "XMLHttpRequest" },
    failOnStatusCode: false,
  });

  // Log in via UI (required — access token lives in-memory Preact signals).
  // storageState will preserve the httpOnly refresh cookie but NOT the
  // in-memory access token. Each test starts with no token — the first API
  // call triggers a transparent refresh via the saved cookie.
  await page.goto("/login");
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("admin123");
  await page.getByRole("button", { name: /sign in/i }).click();

  // Wait for dashboard to confirm auth succeeded
  await page.waitForURL("/");
  await expect(
    page.getByText(/cluster overview|dashboard/i),
  ).toBeVisible();

  // Persist cookies (httpOnly refresh token) + localStorage
  await page.context().storageState({ path: authFile });
});
