import { assertEquals } from "jsr:@std/assert@1";
import { getActiveDomain } from "./constants.ts";

// The Security domain's routes are spread across three URL prefixes:
// /rbac (Access Control + the landing page), /admin (webhooks), and
// /security (Posture). getActiveDomain must resolve all of them to the
// "security" domain so SecondaryNav renders the menu instead of an empty rail.

Deno.test("getActiveDomain: /rbac/overview (security landing) -> security", () => {
  assertEquals(getActiveDomain("/rbac/overview"), "security");
});

Deno.test("getActiveDomain: /rbac index -> security", () => {
  assertEquals(getActiveDomain("/rbac"), "security");
});

Deno.test("getActiveDomain: /rbac/roles -> security", () => {
  assertEquals(getActiveDomain("/rbac/roles"), "security");
});

Deno.test("getActiveDomain: /admin/validatingwebhooks -> security", () => {
  assertEquals(getActiveDomain("/admin/validatingwebhooks"), "security");
});

Deno.test("getActiveDomain: /security/policies -> security", () => {
  assertEquals(getActiveDomain("/security/policies"), "security");
});

// Regression guards for sibling domains that must NOT be swallowed.
Deno.test("getActiveDomain: /workloads/pods -> workloads", () => {
  assertEquals(getActiveDomain("/workloads/pods"), "workloads");
});

Deno.test("getActiveDomain: / -> overview", () => {
  assertEquals(getActiveDomain("/"), "overview");
});
