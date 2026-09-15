import { expect, test } from "bun:test";
import { DNS_LABEL_REGEX, ENV_VAR_NAME_REGEX } from "./wizard-constants.ts";

// Mirrors backend/internal/wizard/regex_parity_test.go (TestRegexParity).
// A drift between Go and TypeScript regex sources produces a failure on
// whichever side was changed, so the next reader knows to update the other.
test("DNS_LABEL_REGEX matches Go dnsLabelRegex source", () => {
  expect(
    DNS_LABEL_REGEX.source,
  ).toBe(
    "^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$",
  );
});

test("ENV_VAR_NAME_REGEX matches Go envVarNameRegex source", () => {
  expect(
    ENV_VAR_NAME_REGEX.source,
  ).toBe(
    "^[A-Za-z_][A-Za-z0-9_]*$",
  );
});
