import { expect } from "@playwright/test";
import type { Page, Route } from "@playwright/test";
import type {
  DashboardLayoutConfig,
  LayoutItem,
} from "../../frontend/lib/dashboard/types.ts";
// Bounds pinned to the Go validator by TestContractParity
// (backend/internal/preferences/parity_test.go), imported rather than
// hardcoded so a change on either side of that parity test shows up here too
// instead of this stub quietly drifting from what it claims to check.
import {
  DASHBOARD_COLUMNS,
  DASHBOARD_LAYOUT_SCHEMA_VERSION,
  DASHBOARD_MAX_ITEMS,
  DASHBOARD_MAX_ROWS,
} from "../../frontend/lib/dashboard/types.ts";

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

/**
 * The cross-cluster listing the editor's "copy from another cluster" reads.
 *
 * A separate glob from `LAYOUT_URL`, and it has to stay one: Playwright
 * matches the whole path, so the scoped route above does not catch this and a
 * spec that stubbed only that one would let the real backend answer here.
 * Every layout spec stubs both, so no test depends on what the shared E2E
 * database happens to hold for the shared login.
 */
export const LAYOUT_LIST_URL = "**/api/v1/preferences/layouts";

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

/**
 * One record as the cross-cluster listing returns it: a stored layout, the
 * cluster it lives on, and anything the server withheld on the way out.
 */
export function listedRecord(
  clusterId: string,
  config: unknown,
  over: { withheld?: string[]; updatedAt?: string; clusterLabel?: string } = {},
) {
  return {
    ...record(1, config),
    id: `00000000-0000-0000-0000-${clusterId.replace(/\W/g, "").padStart(12, "0").slice(-12)}`,
    clusterId,
    ...over,
  };
}

/**
 * Serves the cross-cluster listing. Empty by default, which is what every
 * spec that is not about copying wants: no other cluster's layout, so the
 * editor offers no copy control and the toolbar is the one D15 and D16 tested.
 */
export async function stubLayoutList(
  page: Page,
  records: unknown[] = [],
): Promise<void> {
  await page.route(LAYOUT_LIST_URL, async (route) => {
    const request = route.request();
    if (request.method() !== "GET") {
      await route.fallback();
      return;
    }
    expect(
      request.headers()["x-cluster-id"],
      "the cluster header lib/api.ts injects on every request",
    ).toBeTruthy();
    await json(route, 200, {
      data: records,
      metadata: { total: records.length },
    });
  });
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
 * Asserts a save request is one the real backend's transport contract and
 * structural/geometry rules would accept, and returns its parsed body.
 *
 * A stub that answers every non-GET is a stub that stays green for a client
 * that switched to POST, dropped the JSON body, or lost the headers the API
 * client injects -- all of which `frontend/lib/api.ts` sets and
 * `backend/internal/preferences/handler.go` requires. Checking the contract
 * here is what keeps these tests measuring the client rather than the mock.
 *
 * What this checks, beyond the method and headers: `schemaVersion`, `scope`
 * and `columns` match what the server accepts; the item count stays under
 * `DASHBOARD_MAX_ITEMS`; every item has a non-empty `id` and a non-empty,
 * unique `instanceId`; every item's `x`/`y`/`w`/`h` sit inside the grid (0 to
 * `DASHBOARD_COLUMNS`, 0 to `DASHBOARD_MAX_ROWS`) with a positive area; and no
 * two items occupy overlapping cells. Those are exactly
 * `ValidateDashboardLayout`'s cheap, deterministic, catalog-independent
 * checks (backend/internal/preferences/dashboard.go), mirrored here so a
 * layout the server would answer 400 `invalid_config` for fails in the
 * browser too instead of only ever being caught by a Go unit test.
 *
 * What this deliberately does NOT check: per-widget minimum size (`w`/`h`
 * below the widget's declared `minW`/`minH`), whether `id` names a widget the
 * server's catalog actually knows, or per-widget parameter rules (allowed
 * keys, closed value sets, length/control-character bounds on ad-hoc values
 * like a namespace). All three need the widget catalog, which lives in Go as
 * `allowedWidgets` in dashboard.go and has no TypeScript twin to import here
 * -- reimplementing it would be a second copy that drifts. Those rules are
 * covered by `TestContractParity` and the table tests in
 * `backend/internal/preferences/dashboard_test.go`, not by this stub.
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

  const config = body?.config;
  expect(
    config?.scope,
    "the save must carry the layout config, addressed to a scope",
  ).toBe("overview");
  expect(
    config?.schemaVersion,
    `config.schemaVersion must be ${DASHBOARD_LAYOUT_SCHEMA_VERSION}; the server refuses any other value as unsupported_schema_version`,
  ).toBe(DASHBOARD_LAYOUT_SCHEMA_VERSION);
  expect(
    config?.columns,
    `config.columns must be ${DASHBOARD_COLUMNS}; the server lays out a fixed grid and refuses any other count`,
  ).toBe(DASHBOARD_COLUMNS);

  const items: LayoutItem[] = config?.items ?? [];
  expect(
    items.length,
    `a layout may carry at most ${DASHBOARD_MAX_ITEMS} widgets (got ${items.length}); the server refuses more as invalid_config`,
  ).toBeLessThanOrEqual(DASHBOARD_MAX_ITEMS);

  const seenInstanceIds = new Set<string>();
  for (let i = 0; i < items.length; i++) {
    const item = items[i];
    expect(
      item.id,
      `items[${i}].id must be non-empty; the server refuses a blank widget id`,
    ).toBeTruthy();
    expect(
      item.instanceId,
      `items[${i}].instanceId must be non-empty; the server refuses a blank instance id`,
    ).toBeTruthy();
    expect(
      seenInstanceIds.has(item.instanceId),
      `items[${i}] (${item.id}) repeats instanceId ${JSON.stringify(item.instanceId)}, already used earlier in the layout; the server refuses duplicate instance ids`,
    ).toBe(false);
    seenInstanceIds.add(item.instanceId);

    expect(
      item.w,
      `items[${i}] (${item.id}) has width ${item.w}; widgets must be at least 1 column wide`,
    ).toBeGreaterThanOrEqual(1);
    expect(
      item.h,
      `items[${i}] (${item.id}) has height ${item.h}; widgets must be at least 1 row tall`,
    ).toBeGreaterThanOrEqual(1);
    expect(
      item.x,
      `items[${i}] (${item.id}) starts at column ${item.x}, outside the grid's 0-${DASHBOARD_COLUMNS - 1} range`,
    ).toBeGreaterThanOrEqual(0);
    expect(
      item.x + item.w,
      `items[${i}] (${item.id}) spans columns ${item.x}-${item.x + item.w - 1}, past the grid's ${DASHBOARD_COLUMNS} columns`,
    ).toBeLessThanOrEqual(DASHBOARD_COLUMNS);
    expect(
      item.y,
      `items[${i}] (${item.id}) starts at row ${item.y}, outside the grid's 0-${DASHBOARD_MAX_ROWS - 1} range`,
    ).toBeGreaterThanOrEqual(0);
    expect(
      item.y + item.h,
      `items[${i}] (${item.id}) spans rows ${item.y}-${item.y + item.h - 1}, past the ${DASHBOARD_MAX_ROWS}-row cap the server enforces`,
    ).toBeLessThanOrEqual(DASHBOARD_MAX_ROWS);
  }

  // Overlap is checked after every item's own geometry is sound, so the
  // failure a test sees names the real first problem rather than a garbled
  // pair comparison against an out-of-range item.
  for (let a = 0; a < items.length; a++) {
    for (let b = a + 1; b < items.length; b++) {
      const overlaps = !(
        items[a].x + items[a].w <= items[b].x ||
        items[b].x + items[b].w <= items[a].x ||
        items[a].y + items[a].h <= items[b].y ||
        items[b].y + items[b].h <= items[a].y
      );
      expect(
        overlaps,
        `items[${a}] (${items[a].instanceId}) and items[${b}] (${items[b].instanceId}) occupy overlapping cells; the server refuses overlapping placements`,
      ).toBe(false);
    }
  }

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
  elsewhere: unknown[] = [],
): Promise<StoredWrite[]> {
  const writes: StoredWrite[] = [];
  let stored: StoredWrite | null = initial ?? null;

  // Registered here rather than left to each spec: entering edit mode reads
  // it, so a spec that stubbed only the scoped endpoint would reach the real
  // backend on every Edit click and inherit whatever the shared E2E database
  // holds.
  await stubLayoutList(page, elsewhere);

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

export interface CatalogSourceReply {
  data: unknown;
  metadata?: Record<string, unknown>;
  status?: number;
  message?: string;
}

/**
 * Deterministic P5 source responses, independent of the operators CI runs.
 * Overrides model endpoint outcomes, never widget state: the real cache,
 * shell and parameter wiring run. Layout writes use stubLayoutStore.
 */
export async function stubCatalogSources(
  page: Page,
  overrides: Record<string, CatalogSourceReply> = {},
): Promise<void> {
  const sources: Record<string, CatalogSourceReply> = {
    "/cluster/info": {
      data: { clusterID: "local", platform: "catalog-fixture", nodeCount: 1 },
    },
    "/cluster/dashboard-summary": {
      data: {
        nodes: { total: 1, ready: 1 },
        pods: { total: 0, running: 0, pending: 0, failed: 0 },
        alerts: { active: 0, critical: 0 },
        cpu: null,
        memory: null,
      },
    },
    "/cluster/dashboard-trends": {
      data: { pods: [], cpu: [], memory: [], networkRx: [], networkTx: [] },
    },
    "/resources/events": { data: [], metadata: { total: 0 } },
    "/resources/nodes": {
      data: [
        {
          metadata: { name: "catalog-node" },
          status: { conditions: [{ type: "Ready", status: "True" }] },
        },
      ],
      metadata: { total: 1 },
    },
    "/resources/namespaces": {
      data: ["default", "prod", "staging", "forbidden"].map((name) => ({
        metadata: { name },
      })),
    },
    "/resources/services/prod": { data: [{ metadata: { name: "checkout" } }] },
    "/resources/services/staging": {
      data: [{ metadata: { name: "preview" } }],
    },
    "/resources/services/forbidden": { data: null, status: 403 },
    "/policies/status": { data: { detected: "kyverno" } },
    "/gitops/status": { data: { detected: "" } },
    "/certificates/status": { data: { detected: false } },
    "/mesh/status": { data: { status: { detected: "istio" } } },
    "/externalsecrets/status": { data: { detected: false } },
    "/velero/status": { data: { detected: false } },
    "/scanning/status": { data: { detected: "" } },
    "/storage/snapshot-classes": { data: [], metadata: { available: false } },
    "/gateway/status": { data: { available: false } },
    "/networking/cni": { data: { features: { hubble: false } } },
    "/policies/compliance": { data: { total: 0, pass: 0, fail: 0, warn: 0 } },
    "/policies/compliance/history": { data: [] },
    // The real violations handler serializes its empty Go slice as null.
    "/policies/violations": { data: null },
    ...overrides,
  };
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "");
    const reply: CatalogSourceReply | undefined =
      sources[path] ??
      (/^\/diagnostics\/[^/]+\/summary$/.test(path)
        ? { data: { total: 2, failing: [] } }
        : undefined);
    if (request.method() !== "GET" || reply === undefined) {
      await route.fallback();
      return;
    }
    const status = reply.status ?? 200;
    await json(
      route,
      status,
      status === 200
        ? { data: reply.data, metadata: reply.metadata }
        : {
            error: {
              code: status,
              message: reply.message ?? "Catalog fixture refusal",
            },
          },
    );
  });
}
