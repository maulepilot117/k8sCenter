import { test, expect } from "../fixtures/base.ts";

test.describe("YAML Apply", () => {
  test.beforeEach(async ({ page }) => {
    // Block Monaco CDN so the textarea fallback renders
    await page.route("**/esm.sh/monaco-editor**", (route) => route.abort());
    await page.goto("/tools/yaml-apply");
    await expect(page.getByText("YAML Apply")).toBeVisible();
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
    await textarea.fill(yaml);

    // Assert content was actually filled
    const content = await textarea.inputValue();
    expect(content).toContain("ConfigMap");

    // Click Validate — button should be enabled now (content != placeholder)
    const validateBtn = page.getByRole("button", { name: /validate/i });
    await expect(validateBtn).toBeEnabled();
    await validateBtn.click();

    // Wait for validation to complete (button text changes back from "Validating...")
    await expect(validateBtn).toBeEnabled({ timeout: 10_000 });
  });
});
