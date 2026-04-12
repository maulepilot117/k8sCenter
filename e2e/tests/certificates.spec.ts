import { expect, test } from "../fixtures/base.ts";

test.describe("cert-manager", () => {
  test("certificate list page loads", async ({ page, request }) => {
    const statusResp = await request.get("/api/v1/certificates/status");
    const statusJson = await statusResp.json();
    test.skip(!statusJson.data?.detected, "cert-manager not installed");

    await page.goto("/security/certificates");
    await page.waitForLoadState("networkidle");
    await expect(page.locator("h1")).toContainText("Certificates");

    const rows = page.locator("tbody tr");
    if ((await rows.count()) > 0) {
      await rows.first().locator("a").first().click();
      await expect(page.locator("h1")).toBeVisible();
    }
  });

  test("issuers page loads", async ({ page, request }) => {
    const statusResp = await request.get("/api/v1/certificates/status");
    const statusJson = await statusResp.json();
    test.skip(!statusJson.data?.detected, "cert-manager not installed");

    await page.goto("/security/certificates/issuers");
    await page.waitForLoadState("networkidle");
    await expect(page.locator("h1")).toBeVisible();
  });
});
