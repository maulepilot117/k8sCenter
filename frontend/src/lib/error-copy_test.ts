import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { ERROR_COPY } from "./error-copy.ts";

function readFreshErrorTsx(): string {
  const path = fileURLToPath(
    new URL("../../routes/_error.tsx", import.meta.url),
  );
  return readFileSync(path, "utf-8");
}

test("404 copy matches the Fresh original word for word", () => {
  const source = readFreshErrorTsx();
  expect(source).toContain(`"${ERROR_COPY["404"].heading}"`);
  expect(source).toContain(ERROR_COPY["404"].description);
  expect(ERROR_COPY["404"].showRetry).toBe(false);
});

test("403 copy matches the Fresh original word for word", () => {
  const source = readFreshErrorTsx();
  expect(source).toContain(`"${ERROR_COPY["403"].heading}"`);
  expect(source).toContain(ERROR_COPY["403"].description);
  expect(ERROR_COPY["403"].showRetry).toBe(false);
});

test("500 (generic) copy matches the Fresh original word for word", () => {
  const source = readFreshErrorTsx();
  expect(source).toContain(ERROR_COPY["500"].heading);
  expect(source).toContain(ERROR_COPY["500"].description);
  expect(ERROR_COPY["500"].showRetry).toBe(true);
});

test("status codes are internally consistent with their keys", () => {
  expect(ERROR_COPY["404"].status).toBe(404);
  expect(ERROR_COPY["403"].status).toBe(403);
  expect(ERROR_COPY["500"].status).toBe(500);
});
