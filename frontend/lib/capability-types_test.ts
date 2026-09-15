import { expect, test } from "bun:test";
import type { CapabilityOperationId, ReasonCode } from "./capability-types.ts";
import { CAPABILITY_OPERATION_IDS, REASON_CODES } from "./capability-types.ts";

// Intra-language self-consistency only. This file cannot see the Go source,
// so it can never detect Go/TypeScript drift by itself — that check lives
// in backend/internal/server/capability_parity_test.go
// (TestCapabilityContractParity), which reads this file's source text and
// diffs it against the live Go values. What THIS file verifies is narrower
// but real: that the runtime REASON_CODES / CAPABILITY_OPERATION_IDS arrays
// (what actually ships and executes) agree with the ReasonCode /
// CapabilityOperationId union types derived from them via `typeof
// ...[number]` — i.e. that the two TypeScript-side representations of each
// closed set haven't been edited out of sync with each other. The literals
// below are a fixed expectation for that self-check; if you're trying to
// verify parity with Go, look at the Go-side test instead.

// Compile-time union<->array agreement check. `ReasonCode` (and
// `CapabilityOperationId`) are declared as `typeof REASON_CODES[number]`
// (and the operation-id equivalent), so today they can never drift from the
// array by construction — but that derivation is itself the thing worth
// guarding: if either type is ever hand-redeclared as an independent union
// literal instead, one of these two mutual-assignability directions stops
// type-checking and `deno check` fails right here, before it can silently
// diverge from the runtime array. This is the self-consistency check the Go
// test cannot make (Go only ever reads this file's source text, never the
// TS type system).
function reasonCodeArrayElementIsReasonCode(
  x: (typeof REASON_CODES)[number],
): ReasonCode {
  return x;
}
function reasonCodeIsArrayElement(
  x: ReasonCode,
): (typeof REASON_CODES)[number] {
  return x;
}
function operationIdArrayElementIsOperationId(
  x: (typeof CAPABILITY_OPERATION_IDS)[number],
): CapabilityOperationId {
  return x;
}
function operationIdIsArrayElement(
  x: CapabilityOperationId,
): (typeof CAPABILITY_OPERATION_IDS)[number] {
  return x;
}

test("ReasonCode union type-checks as identical to the REASON_CODES runtime array", () => {
  for (const code of REASON_CODES) {
    expect(reasonCodeArrayElementIsReasonCode(code)).toBe(code);
    expect(reasonCodeIsArrayElement(code)).toBe(code);
  }
});

test("CapabilityOperationId union type-checks as identical to the CAPABILITY_OPERATION_IDS runtime array", () => {
  for (const id of CAPABILITY_OPERATION_IDS) {
    expect(operationIdArrayElementIsOperationId(id)).toBe(id);
    expect(operationIdIsArrayElement(id)).toBe(id);
  }
});

test("REASON_CODES matches the expected literal set (self-consistency; Go parity is checked in capability_parity_test.go)", () => {
  expect([...REASON_CODES].sort()).toEqual([
    "authz_namespace_scoped",
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
  ]);
});

test("CAPABILITY_OPERATION_IDS matches the expected literal set (self-consistency; Go parity is checked in capability_parity_test.go)", () => {
  expect([...CAPABILITY_OPERATION_IDS].sort()).toEqual([
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
  ]);
});
