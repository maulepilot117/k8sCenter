import { expect, test } from "bun:test";
import {
  failingPolicies,
  scopeViolations,
  severityRank,
  worstResources,
} from "./compliance-violations.ts";
import type { NormalizedViolation } from "./policy-types.ts";

function v(
  partial: Partial<NormalizedViolation> & { policy: string; name: string },
): NormalizedViolation {
  return {
    rule: "",
    severity: "medium",
    action: "denied",
    message: "",
    kind: "Pod",
    engine: "kyverno",
    blocking: false,
    ...partial,
  };
}

// --- severityRank ---

test("severityRank: critical is most severe (lowest)", () => {
  expect(severityRank("critical") < severityRank("high")).toBe(true);
  expect(severityRank("high") < severityRank("low")).toBe(true);
});

test("severityRank: unknown sorts last", () => {
  expect(severityRank("bogus") > severityRank("low")).toBe(true);
});

// --- scopeViolations ---

test("scopeViolations: 'all' returns everything", () => {
  const list = [v({ policy: "a", name: "x", namespace: "ns1" })];
  expect(scopeViolations(list, "all").length).toBe(1);
});

test("scopeViolations: filters strictly by namespace", () => {
  const list = [
    v({ policy: "a", name: "x", namespace: "ns1" }),
    v({ policy: "a", name: "y", namespace: "ns2" }),
    v({ policy: "a", name: "z", namespace: undefined }),
  ];
  const out = scopeViolations(list, "ns1");
  expect(out.length).toBe(1);
  expect(out[0].name).toBe("x");
});

// --- failingPolicies ---

test("failingPolicies: groups by policy and counts resources", () => {
  const list = [
    v({ policy: "p1", name: "a" }),
    v({ policy: "p1", name: "b" }),
    v({ policy: "p2", name: "c" }),
  ];
  const out = failingPolicies(list, 5);
  expect(out.length).toBe(2);
  expect(out[0].policy).toBe("p1");
  expect(out[0].count).toBe(2);
});

test("failingPolicies: blocking sorts before higher count", () => {
  const list = [
    v({ policy: "audit", name: "a" }),
    v({ policy: "audit", name: "b" }),
    v({ policy: "audit", name: "c" }),
    v({ policy: "enforce", name: "d", blocking: true }),
  ];
  const out = failingPolicies(list, 5);
  expect(out[0].policy).toBe("enforce");
  expect(out[0].blocking).toBe(true);
});

test("failingPolicies: keeps the most severe severity in a group", () => {
  const list = [
    v({ policy: "p1", name: "a", severity: "low" }),
    v({ policy: "p1", name: "b", severity: "critical" }),
  ];
  const out = failingPolicies(list, 5);
  expect(out[0].severity).toBe("critical");
});

test("failingPolicies: respects the limit", () => {
  const list = [
    v({ policy: "p1", name: "a" }),
    v({ policy: "p2", name: "b" }),
    v({ policy: "p3", name: "c" }),
  ];
  expect(failingPolicies(list, 2).length).toBe(2);
});

// --- worstResources ---

test("worstResources: most severe first, does not mutate input", () => {
  const list = [
    v({ policy: "p", name: "low", severity: "low" }),
    v({ policy: "p", name: "crit", severity: "critical" }),
    v({ policy: "p", name: "med", severity: "medium" }),
  ];
  const out = worstResources(list, 10);
  expect(out.map((x) => x.name)).toEqual(["crit", "med", "low"]);
  // input order preserved (no in-place sort)
  expect(list.map((x) => x.name)).toEqual(["low", "crit", "med"]);
});

test("worstResources: blocking breaks severity ties", () => {
  const list = [
    v({ policy: "p", name: "audit", severity: "high", blocking: false }),
    v({ policy: "p", name: "enforce", severity: "high", blocking: true }),
  ];
  const out = worstResources(list, 10);
  expect(out[0].name).toBe("enforce");
});

test("worstResources: respects the limit", () => {
  const list = [
    v({ policy: "p", name: "a", severity: "critical" }),
    v({ policy: "p", name: "b", severity: "high" }),
    v({ policy: "p", name: "c", severity: "low" }),
  ];
  expect(worstResources(list, 2).length).toBe(2);
});
