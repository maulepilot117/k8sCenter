import { assertEquals } from "jsr:@std/assert@1";
import { DNS_LABEL_REGEX, ENV_VAR_NAME_REGEX } from "./wizard-constants.ts";

// Mirrors backend/internal/wizard/regex_parity_test.go (TestRegexParity).
// A drift between Go and TypeScript regex sources produces a failure on
// whichever side was changed, so the next reader knows to update the other.
Deno.test("DNS_LABEL_REGEX matches Go dnsLabelRegex source", () => {
  assertEquals(
    DNS_LABEL_REGEX.source,
    "^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$",
    "DNS_LABEL_REGEX drifted from backend/internal/wizard/container.go dnsLabelRegex",
  );
});

Deno.test("ENV_VAR_NAME_REGEX matches Go envVarNameRegex source", () => {
  assertEquals(
    ENV_VAR_NAME_REGEX.source,
    "^[A-Za-z_][A-Za-z0-9_]*$",
    "ENV_VAR_NAME_REGEX drifted from backend/internal/wizard/container.go envVarNameRegex",
  );
});
