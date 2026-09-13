import { assertEquals } from "jsr:@std/assert@1";
import { CAPABILITY_OPERATION_IDS, REASON_CODES } from "./capability-types.ts";

// Parity with the Go contract. Each assertion below is pinned to the same
// literal as its counterpart in
// backend/internal/server/capability_parity_test.go
// (TestCapabilityContractParity), so a change on either side fails on that
// side and the message names the file holding the second edit. Comparing
// these constants to each other would prove nothing: both copies live in
// this repo, and the one that matters is the server's.

Deno.test("REASON_CODES matches the Go validReasonCodes set", () => {
  assertEquals(
    [...REASON_CODES].sort(),
    [
      "authz_unknown",
      "cluster_unknown",
      "credentials_invalid",
      "db_unavailable",
      "discovery_missing",
      "discovery_unavailable",
      "forbidden",
      "ok",
      "stale_observation",
      "unreachable",
      "unsupported_platform",
    ],
    "drifted from backend/internal/server/handle_capabilities.go validReasonCodes",
  );
});

Deno.test("CAPABILITY_OPERATION_IDS matches the Go capabilityOperations table", () => {
  assertEquals(
    [...CAPABILITY_OPERATION_IDS].sort(),
    [
      "dashboard.summary",
      "eso.write",
      "flows.stream",
      "logs.search",
      "logs.stream",
      "pod.exec",
      "resources.counts",
      "yaml.apply",
      "yaml.diff",
      "yaml.export",
      "yaml.validate",
    ],
    "drifted from backend/internal/server/handle_capabilities.go capabilityOperations",
  );
});
