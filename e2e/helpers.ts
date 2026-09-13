import {
  type Page,
  type BrowserContext,
  type APIRequestContext,
  expect,
} from "@playwright/test";

/** Get the stored E2E access token from the browser context's localStorage */
export async function getAuthHeaders(
  page: Page,
): Promise<Record<string, string>> {
  const token = await page.evaluate(() =>
    localStorage.getItem("e2e_access_token"),
  );
  return {
    "X-Requested-With": "XMLHttpRequest",
    "Content-Type": "application/json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

/** Generate a unique E2E resource name (8-char random suffix) */
export function e2eName(kind: string): string {
  const rand = Math.random().toString(36).slice(2, 10);
  return `e2e-${kind}-${rand}`;
}

/** Delete a namespaced k8s resource via the API */
export async function deleteResource(
  request: APIRequestContext,
  kind: string,
  namespace: string,
  name: string,
) {
  await request.delete(`/api/v1/resources/${kind}/${namespace}/${name}`, {
    headers: { "X-Requested-With": "XMLHttpRequest" },
    failOnStatusCode: false,
  });
}

/** Delete a cluster-scoped k8s resource via the API */
export async function deleteClusterResource(
  request: APIRequestContext,
  kind: string,
  name: string,
) {
  await request.delete(`/api/v1/resources/${kind}/${name}`, {
    headers: { "X-Requested-With": "XMLHttpRequest" },
    failOnStatusCode: false,
  });
}

/** Wait for the resource table to finish loading (data rows present) */
export async function waitForTableLoaded(page: Page) {
  await expect(page.getByRole("table")).toBeVisible();
  // Wait for spinner to disappear AND at least the table to be stable
  await expect(page.locator(".animate-spin")).not.toBeVisible();
}

/**
 * Shape of a saved-view config, mirroring SavedViewConfig in
 * frontend/lib/preference-types.ts. Declared loosely on purpose: specs seed
 * values the UI would refuse (an unsupported sortKey, for instance) to prove
 * the restore path reports the degradation instead of hiding it.
 */
export type SavedViewConfigSeed = {
  schemaVersion: number;
  resourceKind: string;
  namespace: string;
  search: string;
  statusFilter: string;
  sortKey: string;
  sortDir: string;
};

/**
 * Create a saved view directly through the API and return its record id.
 *
 * Goes through page.request so the call carries the page's own cluster and
 * identity, which is what makes a seeded view belong to the same user the
 * browser is logged in as.
 */
export async function createSavedView(
  page: Page,
  name: string,
  config: SavedViewConfigSeed,
  clusterId?: string,
): Promise<string> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.post("/api/v1/preferences/views", {
    headers: clusterId ? { ...headers, "X-Cluster-ID": clusterId } : headers,
    data: { name, config },
  });
  if (!res.ok()) {
    throw new Error(
      `createSavedView(${name}) failed: ${res.status()} ${await res.text()}`,
    );
  }
  const body = await res.json();
  return body.data.id as string;
}

/**
 * Remove every saved view the current user owns, on every cluster.
 *
 * Cleanup must be exhaustive rather than scoped to the active cluster: a spec
 * that seeds an other-cluster view would otherwise leave it behind, and the
 * 100-view ceiling would eventually start failing unrelated specs.
 */
export async function deleteAllSavedViews(page: Page): Promise<void> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.get("/api/v1/preferences/views", { headers });
  if (!res.ok()) return;
  const body = await res.json();
  for (const record of body.data ?? []) {
    await page.request.delete(`/api/v1/preferences/views/${record.id}`, {
      headers,
      failOnStatusCode: false,
    });
  }
}
