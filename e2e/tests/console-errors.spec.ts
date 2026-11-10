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

const hydrationMismatch =
  "Uncaught Error: Hydration failed because the server rendered HTML didn't " +
  "match the client. As a result this tree will be regenerated on the client.";

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

test("a React hydration error is collected as unexpected", async ({
  page,
  pageErrors,
}) => {
  await serve(page, `console.error(${JSON.stringify(hydrationMismatch)});`);

  const unexpected = pageErrors.unexpected();
  expect(unexpected).toHaveLength(1);
  expect(unexpected[0]?.text).toContain("Hydration failed");

  pageErrors.clear(); // collected on purpose; let teardown pass
});

test.describe("a spec that provokes an error on purpose", () => {
  test.use({ allowedPageErrors: [/effect boom/] });

  test("can allow it by its text", async ({ page, pageErrors }) => {
    await serve(page, throwInEffect);
    expect(pageErrors.entries).toHaveLength(1);
    expect(pageErrors.unexpected()).toEqual([]);
  });
});
