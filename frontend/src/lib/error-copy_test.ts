import { expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  FRESH_ERROR_DESCRIPTIONS,
  FRESH_ERROR_HEADINGS,
} from "./__fixtures__/fresh-parity.ts";
import { ERROR_COPY } from "./error-copy.ts";

/**
 * Same shape as anti-flash-script_test.ts, and for the same reason (U12 step
 * 2b): the frozen fixture is the parity reference, so this guard outlives the
 * Fresh tree, and routes/_error.tsx is additionally checked against the
 * fixture for as long as it is still on disk.
 */
const FRESH_ERROR_TSX = fileURLToPath(
  new URL("../../routes/_error.tsx", import.meta.url),
);

function readFreshErrorTsx(): string | null {
  return existsSync(FRESH_ERROR_TSX)
    ? readFileSync(FRESH_ERROR_TSX, "utf-8")
    : null;
}

for (const status of ["404", "403", "500"] as const) {
  test(`${status} copy matches the frozen Fresh original word for word`, () => {
    expect(ERROR_COPY[status].heading).toBe(FRESH_ERROR_HEADINGS[status]);
    expect(ERROR_COPY[status].description).toBe(
      FRESH_ERROR_DESCRIPTIONS[status],
    );
  });
}

test("only the 500 surface offers a retry", () => {
  expect(ERROR_COPY["404"].showRetry).toBe(false);
  expect(ERROR_COPY["403"].showRetry).toBe(false);
  expect(ERROR_COPY["500"].showRetry).toBe(true);
});

test("status codes are internally consistent with their keys", () => {
  expect(ERROR_COPY["404"].status).toBe(404);
  expect(ERROR_COPY["403"].status).toBe(403);
  expect(ERROR_COPY["500"].status).toBe(500);
});

test("the frozen fixture still matches routes/_error.tsx, while that file exists", () => {
  const source = readFreshErrorTsx();
  if (source === null) return; // Fresh tree deleted; the fixture stands alone.
  for (const status of ["404", "403", "500"] as const) {
    expect(source).toContain(FRESH_ERROR_HEADINGS[status]);
    expect(source).toContain(FRESH_ERROR_DESCRIPTIONS[status]);
  }
});
