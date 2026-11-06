import { afterEach, expect, test } from "bun:test";
import {
  ACTIONS_BY_KIND,
  executeAction,
  getActionMeta,
  getVisibleActions,
  resourcePath,
} from "@/lib/action-handlers.ts";
import { setAccessToken } from "@/lib/api.ts";
import type { K8sResource, RBACSummary } from "@/lib/k8s-types.ts";

const originalFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = originalFetch;
  setAccessToken(null);
});

test("every kind with a delete-capable backend adapter offers Delete", () => {
  for (const kind of [
    "replicasets",
    "services",
    "ingresses",
    "networkpolicies",
    "configmaps",
    "secrets",
    "pvcs",
    "namespaces",
  ]) {
    expect(ACTIONS_BY_KIND[kind]).toContain("delete");
  }
  // Read-only on the backend: the endpoints controller owns them.
  expect(ACTIONS_BY_KIND.endpoints).toBeUndefined();
});

test("a cluster-scoped resource's path has no empty namespace segment", () => {
  expect(resourcePath("namespaces", "", "team-a")).toBe(
    "/v1/resources/namespaces/team-a",
  );
  expect(resourcePath("configmaps", "default", "settings")).toBe(
    "/v1/resources/configmaps/default/settings",
  );
});

test("Delete on a namespace sends DELETE to its cluster-scoped path", async () => {
  setAccessToken("t");
  const calls: { url: string; method?: string }[] = [];
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(input), method: init?.method });
    return Promise.resolve(
      new Response(JSON.stringify({ data: { status: "deleted" } }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;

  await executeAction("delete", "namespaces", "", "team-a");
  expect(calls).toHaveLength(1);
  expect(calls[0].method).toBe("DELETE");
  expect(calls[0].url).toEndWith("/api/v1/resources/namespaces/team-a");
});

test("PVC actions are checked against the persistentvolumeclaims RBAC key", () => {
  const rbac: RBACSummary = {
    clusterScoped: {},
    namespaces: { db: { persistentvolumeclaims: ["get", "delete"] } },
  } as RBACSummary;
  expect(getVisibleActions("pvcs", "db", rbac)).toEqual(["delete"]);
  expect(getVisibleActions("pvcs", "other", rbac)).toEqual([]);
});

test("a namespace delete warns that everything inside goes with it", () => {
  const ns = { metadata: { name: "team-a" } } as K8sResource;
  expect(getActionMeta("delete", ns, "namespaces").confirmMessage).toContain(
    "every resource inside it",
  );
  const cm = { metadata: { name: "settings" } } as K8sResource;
  expect(getActionMeta("delete", cm, "configmaps").confirmMessage).toBe(
    'This will permanently delete "settings".',
  );
});
