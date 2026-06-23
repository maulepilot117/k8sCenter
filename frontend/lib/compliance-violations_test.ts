import { assertEquals } from "jsr:@std/assert@1";
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

Deno.test("severityRank: critical is most severe (lowest)", () => {
  assertEquals(severityRank("critical") < severityRank("high"), true);
  assertEquals(severityRank("high") < severityRank("low"), true);
});

Deno.test("severityRank: unknown sorts last", () => {
  assertEquals(severityRank("bogus") > severityRank("low"), true);
});

// --- scopeViolations ---

Deno.test("scopeViolations: 'all' returns everything", () => {
  const list = [v({ policy: "a", name: "x", namespace: "ns1" })];
  assertEquals(scopeViolations(list, "all").length, 1);
});

Deno.test("scopeViolations: filters strictly by namespace", () => {
  const list = [
    v({ policy: "a", name: "x", namespace: "ns1" }),
    v({ policy: "a", name: "y", namespace: "ns2" }),
    v({ policy: "a", name: "z", namespace: undefined }),
  ];
  const out = scopeViolations(list, "ns1");
  assertEquals(out.length, 1);
  assertEquals(out[0].name, "x");
});

// --- failingPolicies ---

Deno.test("failingPolicies: groups by policy and counts resources", () => {
  const list = [
    v({ policy: "p1", name: "a" }),
    v({ policy: "p1", name: "b" }),
    v({ policy: "p2", name: "c" }),
  ];
  const out = failingPolicies(list, 5);
  assertEquals(out.length, 2);
  assertEquals(out[0].policy, "p1");
  assertEquals(out[0].count, 2);
});

Deno.test("failingPolicies: blocking sorts before higher count", () => {
  const list = [
    v({ policy: "audit", name: "a" }),
    v({ policy: "audit", name: "b" }),
    v({ policy: "audit", name: "c" }),
    v({ policy: "enforce", name: "d", blocking: true }),
  ];
  const out = failingPolicies(list, 5);
  assertEquals(out[0].policy, "enforce");
  assertEquals(out[0].blocking, true);
});

Deno.test("failingPolicies: keeps the most severe severity in a group", () => {
  const list = [
    v({ policy: "p1", name: "a", severity: "low" }),
    v({ policy: "p1", name: "b", severity: "critical" }),
  ];
  const out = failingPolicies(list, 5);
  assertEquals(out[0].severity, "critical");
});

Deno.test("failingPolicies: respects the limit", () => {
  const list = [
    v({ policy: "p1", name: "a" }),
    v({ policy: "p2", name: "b" }),
    v({ policy: "p3", name: "c" }),
  ];
  assertEquals(failingPolicies(list, 2).length, 2);
});

// --- worstResources ---

Deno.test("worstResources: most severe first, does not mutate input", () => {
  const list = [
    v({ policy: "p", name: "low", severity: "low" }),
    v({ policy: "p", name: "crit", severity: "critical" }),
    v({ policy: "p", name: "med", severity: "medium" }),
  ];
  const out = worstResources(list, 10);
  assertEquals(out.map((x) => x.name), ["crit", "med", "low"]);
  // input order preserved (no in-place sort)
  assertEquals(list.map((x) => x.name), ["low", "crit", "med"]);
});

Deno.test("worstResources: blocking breaks severity ties", () => {
  const list = [
    v({ policy: "p", name: "audit", severity: "high", blocking: false }),
    v({ policy: "p", name: "enforce", severity: "high", blocking: true }),
  ];
  const out = worstResources(list, 10);
  assertEquals(out[0].name, "enforce");
});

Deno.test("worstResources: respects the limit", () => {
  const list = [
    v({ policy: "p", name: "a", severity: "critical" }),
    v({ policy: "p", name: "b", severity: "high" }),
    v({ policy: "p", name: "c", severity: "low" }),
  ];
  assertEquals(worstResources(list, 2).length, 2);
});
