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
