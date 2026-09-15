import { expect, test } from "bun:test";
import { getActiveDomain } from "./constants.ts";

// The Security domain's routes are spread across three URL prefixes:
// /rbac (Access Control + the landing page), /admin (webhooks), and
// /security (Posture). getActiveDomain must resolve all of them to the
// "security" domain so SecondaryNav renders the menu instead of an empty rail.

test("getActiveDomain: /rbac/overview (security landing) -> security", () => {
  expect(getActiveDomain("/rbac/overview")).toBe("security");
});

test("getActiveDomain: /rbac index -> security", () => {
  expect(getActiveDomain("/rbac")).toBe("security");
});

test("getActiveDomain: /rbac/roles -> security", () => {
  expect(getActiveDomain("/rbac/roles")).toBe("security");
});

test("getActiveDomain: /admin/validatingwebhooks -> security", () => {
  expect(getActiveDomain("/admin/validatingwebhooks")).toBe("security");
});

test("getActiveDomain: /security/policies -> security", () => {
  expect(getActiveDomain("/security/policies")).toBe("security");
});

// Regression guards for sibling domains that must NOT be swallowed.
test("getActiveDomain: /workloads/pods -> workloads", () => {
  expect(getActiveDomain("/workloads/pods")).toBe("workloads");
});

test("getActiveDomain: / -> overview", () => {
  expect(getActiveDomain("/")).toBe("overview");
});
