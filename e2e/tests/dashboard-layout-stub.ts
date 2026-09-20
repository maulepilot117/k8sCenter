import { expect } from "@playwright/test";
import type { Page, Route } from "@playwright/test";
import type { DashboardLayoutConfig } from "../../frontend/lib/dashboard/types.ts";

// The stored-layout endpoint, stubbed, shared by every dashboard editor spec.
//
// These mock the preferences endpoint rather than using the real one, although
// the E2E stack does run a Postgres. A layout is stored per (user, cluster,
// scope) and the whole suite shares one login, so a single test that really
// saved would hand its arrangement to every later test that loads the
// dashboard -- including the ones in dashboard-grid.spec.ts that assert where
// the default layout puts things. The server side of this contract is covered
// by the handler tests in backend/internal/preferences/handler_test.go.
//
// It lives outside the spec files because the request assertions below are the
// thing that keeps these tests measuring the client rather than the mock, and
// a second copy of them is a copy that drifts.

export const LAYOUT_URL = "**/api/v1/preferences/layouts/overview";

/** A full preference record, the shape `api()` unwraps from `data`. */
export function record(revision: number, config: unknown) {
  return {
    id: "00000000-0000-0000-0000-000000000001",
    kind: "dashboard_layout",
    name: "overview",
    clusterId: "local",
    schemaVersion: 1,
    revision,
    config,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  };
}

export const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

export const preferenceError = (
  route: Route,
  status: number,
  reason: string,
) =>
  json(route, status, {
    error: { code: status, message: "refused", reason },
  });

/**
 * Asserts a save request is one the real backend would accept, and returns its
 * parsed body.
 *
 * A stub that answers every non-GET is a stub that stays green for a client
 * that switched to POST, dropped the JSON body, or lost the headers the API
 * client injects -- all of which `frontend/lib/api.ts` sets and
 * `backend/internal/preferences/handler.go` requires. Checking the contract
 * here is what keeps these tests measuring the client rather than the mock.
 */
export function saveRequestBody(route: Route): {
  revision: number;
  config: DashboardLayoutConfig;
} {
  const request = route.request();
  expect(
    request.method(),
    "the layout save must be a PUT; the endpoint accepts no other method",
  ).toBe("PUT");
  const headers = request.headers();
  expect(
    headers["x-requested-with"],
    "the CSRF header lib/api.ts injects on every non-GET",
  ).toBe("XMLHttpRequest");
  expect(
    headers["x-cluster-id"],
    "the cluster header lib/api.ts injects; layouts are stored per cluster",
  ).toBeTruthy();

  const body = request.postDataJSON() as {
    revision?: unknown;
    config?: DashboardLayoutConfig;
  };
  expect(
    typeof body?.revision,
    "the save must claim a revision; the server uses it for concurrency control",
  ).toBe("number");
  expect(
    body?.config?.scope,
    "the save must carry the layout config, addressed to a scope",
  ).toBe("overview");
  return body as { revision: number; config: DashboardLayoutConfig };
}

export interface StoredWrite {
  revision: number;
  config: DashboardLayoutConfig;
}

/**
 * Serves the layout endpoint from memory: 204 until something is saved, the
 * saved record afterwards, with the revision advancing on each write.
 *
 * `initial` pre-loads the store, for a test that needs the dashboard to start
 * from something other than the shipped default -- a layout missing widgets,
 * so the catalog has something left to add.
 *
 * Returns the writes it saw, so a test can assert what the client actually
 * sent rather than only what came back.
 */
export async function stubLayoutStore(
  page: Page,
  initial?: { revision: number; config: DashboardLayoutConfig },
): Promise<StoredWrite[]> {
  const writes: StoredWrite[] = [];
  let stored: StoredWrite | null = initial ?? null;

  await page.route(LAYOUT_URL, async (route) => {
    const request = route.request();
    if (request.method() === "GET") {
      if (stored === null) {
        await route.fulfill({ status: 204, body: "" });
        return;
      }
      await json(route, 200, { data: record(stored.revision, stored.config) });
      return;
    }
    if (request.method() === "PUT") {
      const body = saveRequestBody(route);
      writes.push(body);
      stored = { revision: body.revision + 1, config: body.config };
      await json(route, 200, { data: record(stored.revision, stored.config) });
      return;
    }
    await route.fallback();
  });

  return writes;
}
