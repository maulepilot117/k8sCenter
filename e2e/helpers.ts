import { type Page, type APIRequestContext, expect } from "@playwright/test";

/** Generate a unique E2E resource name */
export function e2eName(kind: string): string {
  const rand = Math.random().toString(36).slice(2, 6);
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

/** Wait for the resource table to finish loading */
export async function waitForTableLoaded(page: Page) {
  await expect(page.getByRole("table")).toBeVisible();
  await expect(page.locator(".animate-spin")).not.toBeVisible();
}

/** Wait for a toast notification with the given text pattern */
export async function waitForToast(page: Page, text: RegExp | string) {
  const pattern = typeof text === "string" ? new RegExp(text, "i") : text;
  await expect(page.getByText(pattern).first()).toBeVisible();
}
