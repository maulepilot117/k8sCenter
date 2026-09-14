import { expect, test } from "bun:test";
import {
  applyClusterScopedCrdRewrite,
  CLUSTER_SCOPED_CRD_SEGMENT,
  rewriteClusterScopedCrdPathname,
} from "./rewrites.ts";

test("rewrites the cluster-scoped CRD shape onto the routable segment", () => {
  expect(
    rewriteClusterScopedCrdPathname(
      "/extensions/cert-manager.io/certificates/_/my-cert",
    ),
  ).toBe(
    `/extensions/cert-manager.io/certificates/${CLUSTER_SCOPED_CRD_SEGMENT}/my-cert`,
  );
});

test("leaves a namespaced (non-underscore) detail path unchanged", () => {
  const p = "/extensions/cert-manager.io/certificates/prod/my-cert";
  expect(rewriteClusterScopedCrdPathname(p)).toBe(p);
});

test("leaves unrelated paths unchanged", () => {
  for (const p of [
    "/",
    "/extensions",
    "/extensions/cert-manager.io/certificates",
    "/extensions/cert-manager.io/certificates/new",
    "/v1/extensions/resources/g/r/_/n",
    "/extensions/g/r/_/n/extra",
  ]) {
    expect(rewriteClusterScopedCrdPathname(p)).toBe(p);
  }
});

test("only rewrites a literal `_` segment, not a name or group that merely contains an underscore", () => {
  const p = "/extensions/my_group/my_resource/my_ns/my_name";
  expect(rewriteClusterScopedCrdPathname(p)).toBe(p);
});

test("applyClusterScopedCrdRewrite mutates req.url and preserves the query string", () => {
  const req = { url: "/extensions/g/r/_/n?tab=yaml" };
  applyClusterScopedCrdRewrite(req);
  expect(req.url).toBe(
    `/extensions/g/r/${CLUSTER_SCOPED_CRD_SEGMENT}/n?tab=yaml`,
  );
});

test("applyClusterScopedCrdRewrite leaves req.url untouched for a non-matching path", () => {
  const req = { url: "/workloads/pods?namespace=default" };
  applyClusterScopedCrdRewrite(req);
  expect(req.url).toBe("/workloads/pods?namespace=default");
});

test("applyClusterScopedCrdRewrite handles a missing req.url without throwing", () => {
  const req: { url?: string } = {};
  expect(() => applyClusterScopedCrdRewrite(req)).not.toThrow();
});

test("rewrite is not a redirect: the rewritten pathname is still under /extensions/, never a Location-style external target", () => {
  const rewritten = rewriteClusterScopedCrdPathname("/extensions/g/r/_/n");
  expect(rewritten.startsWith("/extensions/")).toBe(true);
});
