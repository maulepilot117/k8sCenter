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
    page.getByRole("heading", { name: /cluster overview/i }),
  ).toBeVisible();

  // Save the in-memory access token to localStorage so storageState persists it.
  // The token normally lives only in a module-level closure (api.ts), so we
  // extract it via the app's exported getter and stash it in localStorage.
  await page.evaluate(() => {
    // Access the token from the api module's exported function
    const token = (globalThis as Record<string, unknown>).__e2eAccessToken;
    if (token) localStorage.setItem("e2e_access_token", token as string);
  });

  // Alternative: extract via network interception — capture the login response token
  // and store it. Since we already logged in above, let's just do a fresh login via API
  // and capture the token directly.
  const loginRes = await page.request.post("/api/v1/auth/login", {
    data: { username: "admin", password: "admin123" },
    headers: {
      "Content-Type": "application/json",
      "X-Requested-With": "XMLHttpRequest",
    },
  });
  if (loginRes.ok()) {
    const body = await loginRes.json();
    const token = body?.data?.accessToken;
    if (token) {
      await page.evaluate(
        (t: string) => localStorage.setItem("e2e_access_token", t),
        token,
      );
    }
  }

  // Persist cookies + localStorage (including e2e_access_token)
  await page.context().storageState({ path: authFile });
});
