import { expect, test } from "../fixtures/base.ts";

// Phase K E2E coverage. The template route + island are independent of ESO
// being installed in the cluster (the YAML editor renders unconditionally;
// only the apply step touches /yaml/apply, which Phase K does not require us
// to exercise here against a kind cluster without ESO). These tests cover the
// gallery rendering, the editor pre-fill on a known provider, and the empty
// state on an unknown provider.

test.describe("eso template gallery and editor", () => {
  test("gallery renders all template providers in alphabetical order", async ({ page }) => {
    await page.goto("/external-secrets/stores/new-from-template");
    await page.waitForLoadState("networkidle");

    await expect(page.locator("h1")).toContainText(
      "Create SecretStore from template",
    );

    // Gallery tiles use display names; verify a known sample appears and that
    // an alphabetically-earlier name renders before an alphabetically-later
    // one. (Full equality on the 11-entry order is brittle; sample-pair
    // ordering is enough to catch a sort regression.)
    await expect(page.getByText("Akeyless")).toBeVisible();
    await expect(page.getByText("Pulumi ESC")).toBeVisible();

    const akeylessIndex = await page.locator("a", { hasText: "Akeyless" })
      .first().evaluate((el) => {
        const anchors = Array.from(
          el.closest("div.grid")?.querySelectorAll("a") ?? [],
        );
        return anchors.indexOf(el as HTMLAnchorElement);
      });
    const pulumiIndex = await page.locator("a", { hasText: "Pulumi ESC" })
      .first().evaluate((el) => {
        const anchors = Array.from(
          el.closest("div.grid")?.querySelectorAll("a") ?? [],
        );
        return anchors.indexOf(el as HTMLAnchorElement);
      });
    expect(akeylessIndex).toBeGreaterThanOrEqual(0);
    expect(pulumiIndex).toBeGreaterThan(akeylessIndex);
  });

  test("gallery tile click navigates to the editor with a pre-filled template", async ({ page }) => {
    await page.goto("/external-secrets/stores/new-from-template");
    await page.waitForLoadState("networkidle");

    await page.locator("a", { hasText: "Akeyless" }).first().click();
    await page.waitForLoadState("networkidle");

    await expect(page).toHaveURL(/\?template=akeyless/);
    await expect(page.locator("h1")).toContainText(
      "Create Akeyless SecretStore",
    );

    // The Monaco editor mounts client-side; assert via the textarea or
    // accessible role rather than depending on Monaco internals. The
    // template's first non-comment line is `apiVersion: external-secrets.io/v1`.
    await expect(page.getByText("apiVersion: external-secrets.io/v1").first())
      .toBeVisible();
  });

  test("unknown template query renders the empty state", async ({ page }) => {
    await page.goto(
      "/external-secrets/stores/new-from-template?template=not-a-real-provider",
    );
    await page.waitForLoadState("networkidle");

    // Empty state surfaces a link back to the gallery.
    await expect(
      page.getByText("No template selected"),
    ).toBeVisible();
    await expect(
      page.locator(
        "a[href='/external-secrets/stores/new-from-template']",
      ),
    ).toBeVisible();
  });
});
