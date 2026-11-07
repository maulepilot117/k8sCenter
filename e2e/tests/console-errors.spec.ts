import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import { PageErrorWatcher } from "../helpers.ts";

// The page-error fixture in fixtures/base.ts, exercised on pages this spec
// serves itself from a made-up origin, so no backend is involved.

const ORIGIN = "http://page-errors.test";

/** Serves one page running `script`, loads it, and waits for it to finish. */
async function serve(page: Page, script: string): Promise<void> {
  await page.route(`${ORIGIN}/**`, (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html><body><main>page</main><script>
        ${script}
        setTimeout(() => console.info("page-done"), 50);
      </script></body></html>`,
    }),
  );
  const done = page.waitForEvent("console", (m) => m.text() === "page-done");
  await page.goto(`${ORIGIN}/`);
  await done;
}

const throwInEffect = `
  // What an island effect that throws looks like: an error on a later task.
  setTimeout(() => { throw new Error("effect boom"); }, 0);
`;

const iconRailMismatch =
  'Expected a DOM node of type "div" but found "" as available DOM-node(s), ' +
  "this is caused by the SSR'd HTML containing different DOM-nodes compared " +
  "to the hydrated one. in div (at src/islands/IconRail.tsx:210) in IconRail";

test("an error thrown in an effect is collected with its text", async ({
  page,
  pageErrors,
}) => {
  await serve(page, throwInEffect);

  const [entry] = pageErrors.unexpected();
  expect(entry?.kind).toBe("pageerror");
  expect(entry?.text).toContain("effect boom");
  expect(PageErrorWatcher.describe(pageErrors.unexpected())).toContain(
    "effect boom",
  );

  pageErrors.clear(); // collected on purpose; let teardown pass
});

test("the fixture fails a test that leaves a page error behind", async ({
  page,
}) => {
  // Expected to fail, at teardown, with "effect boom". If the fixture let it
  // through, the unexpected pass fails the run.
  test.fail();
  await serve(page, throwInEffect);
});

test("a page with no errors passes", async ({ page, pageErrors }) => {
  // Console errors that say nothing about hydration are not failures.
  await serve(
    page,
    `console.error("Failed to load resource: the server responded with a status of 502");
     console.warn("deprecated option");`,
  );
  expect(pageErrors.entries).toEqual([]);
});

test("a known hydration warning passes and an unknown one does not", async ({
  page,
  pageErrors,
}) => {
  await serve(
    page,
    `console.error(${JSON.stringify(iconRailMismatch)});
     console.error(${JSON.stringify(iconRailMismatch.replace(/IconRail/g, "NewIsland"))});`,
  );

  expect(pageErrors.entries).toHaveLength(2);
  const unexpected = pageErrors.unexpected();
  expect(unexpected).toHaveLength(1);
  expect(unexpected[0]?.text).toContain("in NewIsland");

  pageErrors.clear();
});

test.describe("a spec that provokes an error on purpose", () => {
  test.use({ allowedPageErrors: [/effect boom/] });

  test("can allow it by its text", async ({ page, pageErrors }) => {
    await serve(page, throwInEffect);
    expect(pageErrors.entries).toHaveLength(1);
    expect(pageErrors.unexpected()).toEqual([]);
  });
});
