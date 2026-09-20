import { expect, test } from "bun:test";
import type { LayoutRecord, LayoutResponse } from "@/lib/preferences.ts";
import { DEFAULT_OVERVIEW_LAYOUT } from "./default-layout.ts";
import {
  copyableLayouts,
  dropUnknownWidgets,
  layout,
  layoutFromResponse,
  layoutGeneration,
  layoutLoaded,
  layoutRevision,
  layoutUnavailable,
  layoutWithheld,
  loadLayout,
  StaleLayoutScopeError,
  saveLayout,
  WithheldLayoutError,
} from "./layout-store.ts";
import { registerWidget } from "./registry.ts";
import type {
  DashboardLayoutConfig,
  DashboardScope,
  LayoutItem,
  WidgetDef,
} from "./types.ts";
import { DASHBOARD_COLUMNS, DASHBOARD_LAYOUT_SCHEMA_VERSION } from "./types.ts";

// The registry is append-only and `bun test` shares module state across test
// files in one run, so these ids carry the `fixture-` prefix registry_test.ts
// filters on. Without it the catalog drift guard there would see two widgets
// that are not in the shipped catalog and fail whichever file ran second.
const KNOWN = "fixture-layout-store-known";
const ALSO_KNOWN = "fixture-layout-store-also-known";

function defFixture(id: string): WidgetDef {
  return {
    id,
    title: "Fixture",
    family: "cluster",
    scopes: ["overview"],
    sources: ["dashboard-summary"],
    minW: 2,
    minH: 2,
    defaultW: 4,
    defaultH: 4,
    modes: ["normal"],
    render: () => null as unknown as ReturnType<WidgetDef["render"]>,
  };
}

registerWidget(defFixture(KNOWN));
registerWidget(defFixture(ALSO_KNOWN));

function item(instanceId: string, id: string): LayoutItem {
  return { instanceId, id, x: 0, y: 0, w: 4, h: 4 };
}

function config(items: LayoutItem[]): DashboardLayoutConfig {
  return {
    schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
    scope: "overview",
    columns: DASHBOARD_COLUMNS,
    items,
  };
}

test("dropUnknownWidgets: keeps known ids untouched", () => {
  const input = config([item("a", KNOWN), item("b", ALSO_KNOWN)]);
  const { config: kept, warnings } = dropUnknownWidgets(input);

  expect(kept.items).toEqual(input.items);
  expect(warnings).toEqual([]);
  // The envelope travels with the items: a caller rendering `kept` must not
  // have to reach back to the input for the column count it lays out against.
  expect(kept.schemaVersion).toBe(input.schemaVersion);
  expect(kept.scope).toBe(input.scope);
  expect(kept.columns).toBe(input.columns);
});

test("dropUnknownWidgets: drops an unknown id and names it", () => {
  const { config: kept, warnings } = dropUnknownWidgets(
    config([item("a", KNOWN), item("b", "widget-from-the-future")]),
  );

  expect(kept.items.map((i) => i.instanceId)).toEqual(["a"]);
  expect(warnings).toHaveLength(1);
  // The user is told which widget vanished. "Something was removed" is worse
  // than saying nothing: it reports a loss the user cannot act on.
  expect(warnings[0]).toContain("widget-from-the-future");
});

test("dropUnknownWidgets: names every unknown id, not just the first", () => {
  const { config: kept, warnings } = dropUnknownWidgets(
    config([item("a", "gone-one"), item("b", KNOWN), item("c", "gone-two")]),
  );

  expect(kept.items.map((i) => i.instanceId)).toEqual(["b"]);
  expect(warnings).toHaveLength(2);
  expect(warnings.join(" ")).toContain("gone-one");
  expect(warnings.join(" ")).toContain("gone-two");
});

test("dropUnknownWidgets: one warning per missing widget, not per placement", () => {
  // A parameterized widget may legitimately appear twice. Two identical
  // sentences tell the user nothing the first did not, and they collide as
  // list keys wherever the warnings are rendered.
  const { config: kept, warnings } = dropUnknownWidgets(
    config([item("a", "gone-one"), item("b", "gone-one"), item("c", KNOWN)]),
  );

  expect(kept.items.map((i) => i.instanceId)).toEqual(["c"]);
  expect(warnings).toHaveLength(1);
  expect(warnings[0]).toContain("gone-one");
  expect(new Set(warnings).size).toBe(warnings.length);
});

test("dropUnknownWidgets: dropping every widget yields an empty layout, not the default", () => {
  // Falling back to the shipped default here would silently discard a layout
  // the user spent time on because one build was missing its widgets — and
  // the next save would write that default over their arrangement.
  const { config: kept, warnings } = dropUnknownWidgets(
    config([item("a", "gone-one"), item("b", "gone-two")]),
  );

  expect(kept.items).toEqual([]);
  expect(kept.items).not.toEqual(DEFAULT_OVERVIEW_LAYOUT.items);
  expect(warnings).toHaveLength(2);
});

test("dropUnknownWidgets: does not mutate its input", () => {
  const input = config([item("a", KNOWN), item("b", "gone-one")]);
  dropUnknownWidgets(input);

  expect(input.items).toHaveLength(2);
});

test("layoutFromResponse: no stored layout means the scope default at revision 0", () => {
  const loaded = layoutFromResponse(null, "overview");

  // `toBe`, not `toEqual`: loadLayout decides whether to re-mount the grid by
  // comparing this against what the store already holds, so the default has to
  // come back as the same object rather than a copy of it. Clone it here and
  // every unsaved dashboard re-mounts its grid a moment after first paint.
  expect(loaded.config).toBe(DEFAULT_OVERVIEW_LAYOUT);
  // 0 is the claim "I believe none exists", which is what the first save has
  // to send. Any other value would make that save a 409 against a layout the
  // server has no record of.
  expect(loaded.revision).toBe(0);
  expect(loaded.withheld).toEqual([]);
  expect(loaded.warnings).toEqual([]);
});

test("layoutFromResponse: a stored layout carries its revision and drops unknown ids", () => {
  const loaded = layoutFromResponse(
    {
      id: "p1",
      kind: "dashboard_layout",
      name: "overview",
      clusterId: "local",
      schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
      revision: 7,
      config: config([item("a", KNOWN), item("b", "widget-from-the-future")]),
      createdAt: "2026-09-19T00:00:00Z",
      updatedAt: "2026-09-19T00:00:00Z",
    },
    "overview",
  );

  expect(loaded.config.items.map((i) => i.instanceId)).toEqual(["a"]);
  expect(loaded.revision).toBe(7);
  expect(loaded.warnings).toHaveLength(1);
});

test("layoutFromResponse: an omitted withheld key reads as an empty list", () => {
  // `withheld` is `omitempty` on the Go side, so an unfiltered response has no
  // such key at all. Passing that `undefined` through would make every
  // ordinary load throw at the first `.length`.
  const loaded = layoutFromResponse(
    {
      id: "p1",
      kind: "dashboard_layout",
      name: "overview",
      clusterId: "local",
      schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
      revision: 1,
      config: config([item("a", KNOWN)]),
      createdAt: "2026-09-19T00:00:00Z",
      updatedAt: "2026-09-19T00:00:00Z",
    },
    "overview",
  );

  expect(loaded.withheld).toEqual([]);
});

test("layoutFromResponse: withheld instanceIds survive to the caller", () => {
  const loaded = layoutFromResponse(
    {
      id: "p1",
      kind: "dashboard_layout",
      name: "overview",
      clusterId: "local",
      schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
      revision: 3,
      config: config([item("a", KNOWN)]),
      createdAt: "2026-09-19T00:00:00Z",
      updatedAt: "2026-09-19T00:00:00Z",
      withheld: ["b", "c"],
    },
    "overview",
  );

  expect(loaded.withheld).toEqual(["b", "c"]);
  // The withheld placements are NOT in the config — the server already removed
  // them. That is exactly why the list has to arrive separately.
  expect(loaded.config.items.map((i) => i.instanceId)).toEqual(["a"]);
});

test("saveLayout: refuses to write back a layout the server filtered", async () => {
  // The withheld list is armed by a REAL filtered response travelling the whole
  // chain -- server body -> layoutFromResponse -> loadLayout -> the signal --
  // never by setting the signal by hand. Hand-setting it proves only that
  // saveLayout reads a signal; it would keep passing if layoutFromResponse
  // stopped propagating `withheld` at all, which is the one regression that
  // would silently disarm the only guard standing between a revoked namespace
  // and a permanent delete.
  const stored = config([item("a", KNOWN)]);
  await withFetch(
    [() => jsonResponse(layoutRecord(6, stored, ["d-prod-diagnostics"]))],
    () => loadLayout("overview"),
  );
  expect(layoutWithheld.value).toEqual(["d-prod-diagnostics"]);

  const before = { layout: layout.value, revision: layoutRevision.value };

  let threw: unknown;
  let requests = 0;
  const realFetch = globalThis.fetch;
  globalThis.fetch = (() => {
    requests += 1;
    return Promise.resolve(jsonResponse(layoutRecord(7, stored)));
  }) as unknown as typeof globalThis.fetch;
  try {
    await saveLayout("overview", stored).catch((e) => {
      threw = e;
    });
  } finally {
    globalThis.fetch = realFetch;
  }

  expect(threw).toBeInstanceOf(WithheldLayoutError);
  // The refusal happens before the request: the stored layout still holds the
  // withheld placements, and a PUT carrying the filtered config would pass the
  // revision check and delete them permanently.
  expect(requests).toBe(0);
  // A refused save must not look like an applied one.
  expect(layout.value).toBe(before.layout);
  expect(layoutRevision.value).toBe(before.revision);
});

test("saveLayout: the refusal names what would have been lost", async () => {
  const stored = config([item("a", KNOWN)]);
  await withFetch(
    [() => jsonResponse(layoutRecord(2, stored, ["d-prod-diagnostics"]))],
    () => loadLayout("overview"),
  );

  try {
    await saveLayout("overview", stored);
    throw new Error("expected saveLayout to reject");
  } catch (err) {
    expect(err).toBeInstanceOf(WithheldLayoutError);
    expect((err as WithheldLayoutError).withheld).toEqual([
      "d-prod-diagnostics",
    ]);
    expect((err as Error).message).toContain("d-prod-diagnostics");
  }
});

// ---------------------------------------------------------------------------
// The async paths
//
// lib/preferences.ts is transport over lib/api.ts and has no injectable fetch,
// so these stub `globalThis.fetch` around a real call, the way
// preferences_test.ts does. The restore is in a `finally` because `bun test`
// shares module state across files in one run: a stub left installed would
// follow every later test file in the same run.
//
// The signals are module-global for the same reason, so each test below
// establishes the state it needs through a real load rather than assuming the
// state the previous test left behind.
// ---------------------------------------------------------------------------

function layoutRecord(
  revision: number,
  cfg: DashboardLayoutConfig,
  withheld?: string[],
) {
  return {
    id: "p1",
    kind: "dashboard_layout",
    name: "overview",
    clusterId: "local",
    schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
    revision,
    config: cfg,
    createdAt: "2026-09-19T00:00:00Z",
    updatedAt: "2026-09-19T00:00:00Z",
    ...(withheld ? { withheld } : {}),
  };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data: body }), {
    status,
    headers: { "content-type": "application/json" },
  });
}

/** Swaps in a fetch that answers with `responses` in order, and restores it. */
async function withFetch<T>(
  responses: Array<() => Response | Promise<Response>>,
  run: () => Promise<T>,
): Promise<{ result: T; calls: number }> {
  const realFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (() => {
    const next = responses[calls] ?? responses[responses.length - 1];
    calls += 1;
    return Promise.resolve(next());
  }) as unknown as typeof globalThis.fetch;
  try {
    return { result: await run(), calls };
  } finally {
    globalThis.fetch = realFetch;
  }
}

test("loadLayout: an unsaved scope keeps the default and does not re-mount the grid", async () => {
  // Establish the precondition rather than inherit it. The claim here is that a
  // 204 over a store ALREADY showing the default is a no-op for the grid's
  // mount key; if an earlier test left a stored layout in place, the 204 really
  // does replace it and the bump is correct. `bun test` shares module state
  // across files, so this test must not depend on what ran before it.
  layout.value = DEFAULT_OVERVIEW_LAYOUT;
  const before = layoutGeneration.value;
  await withFetch([() => new Response(null, { status: 204 })], () =>
    loadLayout("overview"),
  );

  expect(layout.value).toBe(DEFAULT_OVERVIEW_LAYOUT);
  expect(layoutRevision.value).toBe(0);
  expect(layoutLoaded.value).toBe(true);
  expect(layoutUnavailable.value).toBe(undefined);
  // The grid is already rendering this exact object. Re-mounting it would
  // discard a drag the user began before the response landed.
  expect(layoutGeneration.value).toBe(before);
});

test("loadLayout: a stored layout replaces the default and re-mounts once", async () => {
  const before = layoutGeneration.value;
  const stored = config([item("a", KNOWN)]);
  await withFetch([() => jsonResponse(layoutRecord(7, stored))], () =>
    loadLayout("overview"),
  );

  expect(layout.value.items.map((i) => i.instanceId)).toEqual(["a"]);
  expect(layoutRevision.value).toBe(7);
  expect(layoutWithheld.value).toEqual([]);
  expect(layoutGeneration.value).toBe(before + 1);
});

test("loadLayout: a failed load settles, keeps the layout, and names the reason", async () => {
  // Establish a known-good state first, so "left untouched" is a claim with
  // something behind it rather than a coincidence of the default.
  const stored = config([item("a", KNOWN)]);
  await withFetch([() => jsonResponse(layoutRecord(3, stored))], () =>
    loadLayout("overview"),
  );
  const kept = layout.value;
  // Put the flag back to its pre-load value, so the assertion below can
  // actually fail. Asserting `true` when the successful load above already set
  // it proves nothing about the catch path, which is the whole subject here.
  layoutLoaded.value = false;

  await withFetch(
    [
      () =>
        new Response(
          JSON.stringify({
            error: {
              code: 503,
              message: "no database",
              reason: "database_unavailable",
            },
          }),
          { status: 503, headers: { "content-type": "application/json" } },
        ),
    ],
    () => loadLayout("overview"),
  );

  expect(layoutUnavailable.value).toBe("database_unavailable");
  // A transient failure must not blank a dashboard the user was looking at.
  expect(layout.value).toBe(kept);
  expect(layoutRevision.value).toBe(3);
  // Settled, not still-loading: "asked, and could not be told" is an answer.
  // This is the assertion the reset above makes meaningful.
  expect(layoutLoaded.value).toBe(true);
});

test("loadLayout: a reason-less failure is 'unknown', never undefined", async () => {
  await withFetch(
    [() => Promise.reject(new TypeError("Failed to fetch"))],
    () => loadLayout("overview"),
  );

  // undefined would mean "nothing went wrong", which is the one thing this
  // signal must never say about a failure.
  expect(layoutUnavailable.value).toBe("unknown");
});

test("loadLayout: an already-aborted caller signal issues no request", async () => {
  const ac = new AbortController();
  ac.abort();

  const { calls } = await withFetch(
    [() => new Response(null, { status: 204 })],
    () => loadLayout("overview", ac.signal),
  );

  // Not merely wasteful: falling through would also abort a healthy load
  // already in flight on the way past.
  expect(calls).toBe(0);
});

test("saveLayout: refuses a scope the store has not observed", async () => {
  await withFetch([() => new Response(null, { status: 204 })], () =>
    loadLayout("overview"),
  );

  // A stand-in scope, because only one ships and the guard is otherwise
  // unreachable. The revision and withheld list in the store describe
  // "overview"; against any other scope they are claims about another record.
  const otherScope = "not-a-served-scope" as DashboardScope;
  let threw: unknown;
  await saveLayout(otherScope, config([item("a", KNOWN)])).catch((e) => {
    threw = e;
  });

  expect(threw).toBeInstanceOf(StaleLayoutScopeError);
  expect((threw as Error).message).toContain("not-a-served-scope");
});

test("saveLayout: a successful write commits the server's new revision", async () => {
  const stored = config([item("a", KNOWN)]);
  await withFetch([() => jsonResponse(layoutRecord(4, stored))], () =>
    loadLayout("overview"),
  );

  const next = config([item("a", KNOWN), item("b", ALSO_KNOWN)]);
  await withFetch([() => jsonResponse(layoutRecord(5, next))], () =>
    saveLayout("overview", next),
  );

  expect(layout.value).toBe(next);
  expect(layoutRevision.value).toBe(5);
  expect(layoutLoaded.value).toBe(true);
  expect(layoutUnavailable.value).toBe(undefined);
});

test("saveLayout: a read still in flight cannot roll the revision back", async () => {
  const stored = config([item("a", KNOWN)]);
  await withFetch([() => jsonResponse(layoutRecord(4, stored))], () =>
    loadLayout("overview"),
  );

  // A load whose response is held open, so a save can complete underneath it.
  let releaseRead!: () => void;
  const readLanded = new Promise<void>((r) => {
    releaseRead = r;
  });

  const realFetch = globalThis.fetch;
  const next = config([item("a", KNOWN), item("b", ALSO_KNOWN)]);
  try {
    globalThis.fetch = ((_input: unknown, init?: RequestInit) => {
      if (init?.method === "PUT") {
        return Promise.resolve(jsonResponse(layoutRecord(5, next)));
      }
      // The stale read: it describes revision 4, the state before the save.
      return readLanded.then(() => jsonResponse(layoutRecord(4, stored)));
    }) as unknown as typeof globalThis.fetch;

    const pendingRead = loadLayout("overview");
    await saveLayout("overview", next);
    expect(layoutRevision.value).toBe(5);

    releaseRead();
    await pendingRead;
  } finally {
    globalThis.fetch = realFetch;
  }

  // The read resolved last, but it describes a state the save already moved
  // past. Applying it would leave the next save claiming revision 4 against a
  // record at 5 -- a 409 the user never caused, over a layout they did save.
  expect(layoutRevision.value).toBe(5);
  expect(layout.value).toBe(next);
});

test("loadLayout: a failure that lost the race does not report itself", async () => {
  // The catch path's mirror of the success path's staleness check. Without it,
  // a read that errors after a save committed tells the user their layout could
  // not be loaded and disables the editor, seconds after they saved it.
  const stored = config([item("a", KNOWN)]);
  await withFetch([() => jsonResponse(layoutRecord(4, stored))], () =>
    loadLayout("overview"),
  );

  let releaseRead!: () => void;
  const readFailed = new Promise<void>((r) => {
    releaseRead = r;
  });
  const next = config([item("a", KNOWN), item("b", ALSO_KNOWN)]);

  const realFetch = globalThis.fetch;
  try {
    globalThis.fetch = ((_input: unknown, init?: RequestInit) => {
      if (init?.method === "PUT") {
        return Promise.resolve(jsonResponse(layoutRecord(5, next)));
      }
      return readFailed.then(() => Promise.reject(new TypeError("boom")));
    }) as unknown as typeof globalThis.fetch;

    const pendingRead = loadLayout("overview");
    await saveLayout("overview", next);
    releaseRead();
    await pendingRead;
  } finally {
    globalThis.fetch = realFetch;
  }

  // The save stands, and nothing tells the user it failed.
  expect(layoutUnavailable.value).toBe(undefined);
  expect(layoutRevision.value).toBe(5);
  expect(layout.value).toBe(next);
});

test("saveLayout: a request that fails after sending invalidates the observation", async () => {
  // The server may have committed and failed on the way back. Reusing the old
  // revision afterwards would be refused as a conflict the user did not cause,
  // so the next save must be forced through a fresh read instead.
  const stored = config([item("a", KNOWN)]);
  await withFetch([() => jsonResponse(layoutRecord(8, stored))], () =>
    loadLayout("overview"),
  );

  const realFetch = globalThis.fetch;
  let threw: unknown;
  try {
    globalThis.fetch = (() =>
      Promise.reject(
        new TypeError("connection reset"),
      )) as unknown as typeof globalThis.fetch;
    await saveLayout("overview", stored).catch((e) => {
      threw = e;
    });
  } finally {
    globalThis.fetch = realFetch;
  }
  expect(threw).toBeInstanceOf(TypeError);

  // The observation is gone, so a retry is refused rather than replaying a
  // revision the server may already have moved past.
  let second: unknown;
  await saveLayout("overview", stored).catch((e) => {
    second = e;
  });
  expect(second).toBeInstanceOf(StaleLayoutScopeError);
});

test("saveLayout: a failed read drops the observation the guard depends on", async () => {
  // A read that failed learned nothing, so the withheld list it left behind
  // describes a world that may have moved on. That is the case the peer review
  // named: without this, a failed reload goes on authorizing saves against a
  // withheld list nobody has re-checked since access was last confirmed.
  const stored = config([item("a", KNOWN)]);
  await withFetch([() => jsonResponse(layoutRecord(9, stored))], () =>
    loadLayout("overview"),
  );

  await withFetch([() => Promise.reject(new TypeError("boom"))], () =>
    loadLayout("overview"),
  );

  let threw: unknown;
  await saveLayout("overview", stored).catch((e) => {
    threw = e;
  });
  expect(threw).toBeInstanceOf(StaleLayoutScopeError);
});

test("loadLayout: an already-aborted signal settles nothing", async () => {
  // Pins the documented contract. No request is made, so there is nothing to
  // report and layoutLoaded keeps whatever it held. A future caller that aborts
  // while STAYING mounted has to issue a replacement load; this is the test
  // that will go red if someone changes that without meaning to.
  await withFetch([() => new Response(null, { status: 204 })], () =>
    loadLayout("overview"),
  );
  const before = layoutLoaded.value;

  const ac = new AbortController();
  ac.abort();
  const { calls } = await withFetch(
    [() => new Response(null, { status: 204 })],
    () => loadLayout("overview", ac.signal),
  );

  expect(calls).toBe(0);
  expect(layoutLoaded.value).toBe(before);
});

// ---------------------------------------------------------------------------
// copyableLayouts (D17): which of a user's other clusters' layouts can be taken
// ---------------------------------------------------------------------------

/** One record as the list endpoint returns it. Distinct from `layoutRecord`
 * above: that one is a single-scope read, keyed by revision. */
function listedLayout(
  clusterId: string,
  cfg: DashboardLayoutConfig,
  over: Partial<LayoutRecord> & { withheld?: string[] } = {},
): LayoutResponse {
  return {
    id: `rec-${clusterId}`,
    kind: "dashboard_layout",
    name: "overview",
    clusterId,
    schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
    revision: 1,
    config: cfg,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-02T00:00:00Z",
    ...over,
  };
}

test("copyableLayouts: offers another cluster's layout and not this one's", () => {
  const got = copyableLayouts(
    [
      listedLayout("prod-east", config([item("a", KNOWN)])),
      listedLayout("local", config([item("b", ALSO_KNOWN)])),
    ],
    "overview",
    "local",
    DASHBOARD_COLUMNS,
  );

  expect(got.map((c) => c.clusterId)).toEqual(["prod-east"]);
  expect(got[0].config.items.map((i) => i.id)).toEqual([KNOWN]);
  expect(got[0].warnings).toEqual([]);
});

test("copyableLayouts: input order is preserved", () => {
  const got = copyableLayouts(
    [
      listedLayout("b-cluster", config([item("a", KNOWN)])),
      listedLayout("a-cluster", config([item("b", KNOWN)])),
    ],
    "overview",
    "local",
    DASHBOARD_COLUMNS,
  );

  // The endpoint sorts by updated_at descending, and most-recently-arranged is
  // the order a user looking for a layout they just made wants. Re-sorting
  // here by cluster name would bury it.
  expect(got.map((c) => c.clusterId)).toEqual(["b-cluster", "a-cluster"]);
});

test("copyableLayouts: skips a layout addressed to another dashboard", () => {
  const otherScope = {
    ...config([item("a", KNOWN)]),
    scope: "workloads" as DashboardScope,
  };

  expect(
    copyableLayouts(
      [listedLayout("prod-east", otherScope)],
      "overview",
      "local",
      DASHBOARD_COLUMNS,
    ),
  ).toEqual([]);
});

test("copyableLayouts: skips a layout this build cannot read", () => {
  const future = {
    ...config([item("a", KNOWN)]),
    schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION + 1,
  };

  expect(
    copyableLayouts(
      [listedLayout("prod-east", future)],
      "overview",
      "local",
      DASHBOARD_COLUMNS,
    ),
  ).toEqual([]);
});

test("copyableLayouts: skips a layout laid out on a different grid", () => {
  const narrower = { ...config([item("a", KNOWN)]), columns: 6 };

  expect(
    copyableLayouts(
      [listedLayout("prod-east", narrower)],
      "overview",
      "local",
      DASHBOARD_COLUMNS,
    ),
  ).toEqual([]);
});

test("copyableLayouts: drops widgets this build has no definition for", () => {
  const got = copyableLayouts(
    [listedLayout("prod-east", config([item("a", KNOWN), item("b", "gone")]))],
    "overview",
    "local",
    DASHBOARD_COLUMNS,
  );

  expect(got).toHaveLength(1);
  expect(got[0].config.items.map((i) => i.id)).toEqual([KNOWN]);
  expect(got[0].warnings).toHaveLength(1);
  expect(got[0].warnings[0]).toContain("gone");
});

test("copyableLayouts: says when the server withheld placements", () => {
  const got = copyableLayouts(
    [
      listedLayout("prod-east", config([item("a", KNOWN)]), {
        withheld: ["scoped-one", "scoped-two"],
      }),
    ],
    "overview",
    "local",
    DASHBOARD_COLUMNS,
  );

  expect(got).toHaveLength(1);
  // An absent observation must not look like an empty one: a layout that
  // arrives short two widgets has to say so, or the copy looks like it lost
  // them.
  expect(got[0].warnings).toHaveLength(1);
  expect(got[0].warnings[0]).toContain("2");
});

test("copyableLayouts: skips a layout with nothing left to copy", () => {
  // Every widget on it is one this build does not have, so taking it would
  // replace the dashboard with an empty grid and a warning. That is not an
  // offer worth making, and a row that empties the screen is worse than no row.
  expect(
    copyableLayouts(
      [listedLayout("prod-east", config([item("a", "gone")]))],
      "overview",
      "local",
      DASHBOARD_COLUMNS,
    ),
  ).toEqual([]);
});

test("copyableLayouts: tolerates a record whose config is not a layout", () => {
  const junk = { schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION } as unknown;

  expect(
    copyableLayouts(
      [listedLayout("prod-east", junk as DashboardLayoutConfig)],
      "overview",
      "local",
      DASHBOARD_COLUMNS,
    ),
  ).toEqual([]);
});

test("copyableLayouts: the offered config does not alias the record", () => {
  const source = config([item("a", KNOWN)]);
  const got = copyableLayouts(
    [listedLayout("prod-east", source)],
    "overview",
    "local",
    DASHBOARD_COLUMNS,
  );

  // The island hands this straight to a session and a grid, both of which
  // rebuild items rather than reshaping them -- but the record it came from is
  // a fetch result the dialog is still rendering, and a shared array would let
  // one become the other's surprise.
  expect(got[0].config.items).not.toBe(source.items);
});
