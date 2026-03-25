import { test, expect } from "../fixtures/base.ts";

test.describe("YAML Apply", () => {
  test.beforeEach(async ({ page }) => {
    // Block Monaco CDN so the textarea fallback renders
    await page.route("**/esm.sh/monaco-editor**", (route) => route.abort());
    await page.goto("/tools/yaml-apply");
    await expect(page.getByText("YAML Apply")).toBeVisible();
  });

  test("page loads with editor", async ({ page }) => {
    // Should show the page heading and either Monaco or textarea fallback
    await expect(page.getByText("YAML Apply")).toBeVisible();
    await expect(
      page.getByText(/paste.*yaml|apply.*kubernetes/i),
    ).toBeVisible();
  });

  test("validates valid YAML", async ({ page }) => {
    const yaml = [
      "apiVersion: v1",
      "kind: ConfigMap",
      "metadata:",
      "  name: e2e-validate-test",
      "  namespace: default",
      "data:",
      "  key: value",
    ].join("\n");

    // Fill the fallback textarea (Monaco CDN is blocked)
    const textarea = page.locator("textarea");
    if (await textarea.isVisible().catch(() => false)) {
      await textarea.fill(yaml);
    } else {
      // If no textarea fallback, try clicking into the editor area and typing
      await page.locator(".cm-editor, .monaco-editor").first().click();
      await page.keyboard.press("Meta+a");
      await page.keyboard.type(yaml, { delay: 5 });
    }

    // Click Validate
    const validateBtn = page.getByRole("button", { name: /validate/i });
    await expect(validateBtn).toBeEnabled();
    await validateBtn.click();

    // Should not show a validation error for valid YAML
    await expect(page.getByText(/error/i)).not.toBeVisible({ timeout: 5_000 });
  });
});
