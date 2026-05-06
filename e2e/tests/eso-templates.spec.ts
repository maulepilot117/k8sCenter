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

    // Gallery tiles each carry the provider's display name in a title span.
    // Scope to those titles via the link's href to avoid colliding with the
    // tile's description paragraph (which often repeats the display name)
    // and the nav-rail entry that points at this same route.
    const akeylessTile = page.locator(
      "a[href='/external-secrets/stores/new-from-template?template=akeyless']",
    );
    const pulumiTile = page.locator(
      "a[href='/external-secrets/stores/new-from-template?template=pulumi']",
    );
    await expect(akeylessTile).toBeVisible();
    await expect(pulumiTile).toBeVisible();

    // Sort regression check: alphabetically Akeyless precedes Pulumi ESC.
    // Compute each tile's index within the gallery grid; akeyless < pulumi.
    const akeylessIndex = await akeylessTile.evaluate((el) => {
      const anchors = Array.from(
        el.closest("div.grid")?.querySelectorAll("a") ?? [],
      );
      return anchors.indexOf(el as HTMLAnchorElement);
    });
    const pulumiIndex = await pulumiTile.evaluate((el) => {
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

    await page.locator(
      "a[href='/external-secrets/stores/new-from-template?template=akeyless']",
    ).click();
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

    // Empty state surfaces a link back to the gallery. The same href appears
    // in the nav-rail's "Create from template" entry, so scope to the empty-
    // state container by anchor text rather than href alone.
    await expect(
      page.getByText("No template selected"),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "template gallery" }),
    ).toBeVisible();
  });
});
