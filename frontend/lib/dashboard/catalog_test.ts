import { describe, expect, test } from "bun:test";
import {
  ALREADY_PLACED,
  DASHBOARD_FULL,
  disabledReasonFor,
  NO_ROOM,
  NOT_INSTALLED,
  NOT_PERMITTED,
  selectionAfterEntriesChange,
} from "./catalog.ts";
import type { SourceState } from "./data.ts";
import type { LayoutItem, WidgetDef } from "./types.ts";
import {
  DASHBOARD_COLUMNS,
  DASHBOARD_MAX_ITEMS,
  DASHBOARD_MAX_ROWS,
} from "./types.ts";

// Why one catalog row is offered or refused. The palette itself is a
// component and therefore untested by construction (D-10); this is the part
// of "Add a widget" that decides what a user actually sees -- a row that
// looks addable but silently does nothing, or one refused for the wrong
// reason -- so it lives here instead.

function def(
  id: string,
  defaultW: number,
  defaultH: number,
  over: Partial<WidgetDef> = {},
): WidgetDef {
  return {
    id,
    title: id,
    family: "cluster",
    scopes: ["overview"],
    sources: ["dashboard-summary"],
    minW: 2,
    minH: 2,
    defaultW,
    defaultH,
    modes: ["normal"],
    render: () => null as unknown as ReturnType<WidgetDef["render"]>,
    ...over,
  };
}

/** `id` defaults to a placement of `def("id", ...)` with the same widget id
 * unless overridden, so a test can place a copy of the widget under test just
 * by reusing its instanceId as the item id's base. */
function item(
  instanceId: string,
  x: number,
  y: number,
  w: number,
  h: number,
  id: string = `w-${instanceId}`,
): LayoutItem {
  return { instanceId, id, x, y, w, h };
}

/** `n` items with distinct instance ids, none of them `widgetId`, sized and
 * positioned so their content is irrelevant -- only the count matters to the
 * item-cap check, which runs before anything else looks at the layout. */
function fillerItems(n: number, widgetId = "filler"): LayoutItem[] {
  return Array.from({ length: n }, (_, i) =>
    item(`${widgetId}-${i}`, 0, i, 1, 1, `${widgetId}-${i}`),
  );
}

describe("disabledReasonFor", () => {
  test("a widget that can be added returns null", () => {
    expect(
      disabledReasonFor(def("nodes", 4, 4), [], DASHBOARD_COLUMNS),
    ).toBeNull();
  });

  test("an unparameterized widget already placed reports ALREADY_PLACED", () => {
    const d = def("cluster-health", 4, 4);
    const placed = [item("a", 0, 0, 4, 4, "cluster-health")];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(
      ALREADY_PLACED,
    );
  });

  test("a widget with nowhere to fit reports NO_ROOM", () => {
    const d = def("new", 4, 4);
    // Fills the entire grid up to the row cap, so nothing can be placed --
    // same fixture shape as placement_test.ts's "already full to the row cap"
    // case, since this reason is a wrapper around that same scan.
    const placed = [item("a", 0, 0, DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS)];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(NO_ROOM);
  });

  test("a full dashboard reports DASHBOARD_FULL", () => {
    const d = def("new", 4, 4);
    const placed = fillerItems(DASHBOARD_MAX_ITEMS);
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(
      DASHBOARD_FULL,
    );
  });

  test("already-placed beats no-room when both apply, without paying for a scan", () => {
    // The whole grid is full (so a scan would also return NO_ROOM), and a
    // copy of this exact unparameterized widget is already on it. The
    // already-placed reason must win: it is the cheap membership check, and
    // it is checked before the placement scan runs at all.
    const d = def("cluster-health", DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS);
    const placed = [
      item("a", 0, 0, DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS, "cluster-health"),
    ];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(
      ALREADY_PLACED,
    );
  });

  test("the item cap beats already-placed and no-room together", () => {
    // A full dashboard (DASHBOARD_MAX_ITEMS items) that also happens to
    // already contain this exact widget and has no free cell for it either --
    // every reason applies, and DASHBOARD_FULL must be the one reported,
    // because it is the most general: a full dashboard refuses this widget
    // for a reason that has nothing to do with the widget itself.
    const d = def("cluster-health", 1, 1);
    const placed = [
      item("cluster-health", 0, 0, 1, 1, "cluster-health"),
      ...fillerItems(DASHBOARD_MAX_ITEMS - 1),
    ];
    expect(placed).toHaveLength(DASHBOARD_MAX_ITEMS);
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(
      DASHBOARD_FULL,
    );
  });

  test("a parameterized widget is never refused for being a duplicate", () => {
    // Two placed copies of the same parameterized widget -- prod beside
    // staging -- is the case `params` exists for. With room left, a third
    // copy is addable.
    const d = def("diagnostics", 2, 2, { params: { namespace: [] } });
    const placed = [
      item("a", 0, 0, 2, 2, "diagnostics"),
      item("b", 2, 0, 2, 2, "diagnostics"),
    ];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBeNull();
  });

  test("a parameterized widget is still refused when there is no room", () => {
    const d = def("diagnostics", DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS, {
      params: { namespace: [] },
    });
    const placed = [
      item("a", 0, 0, DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS, "diagnostics"),
    ];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(NO_ROOM);
  });

  test("the columns argument changes the answer", () => {
    // `a` occupies the left half of the grid for the widget's full height.
    // On a twelve-column grid, six columns remain free on the right and the
    // widget fits there. On a six-column grid, `a` alone spans the entire
    // width, so the same layout has nowhere left to place it.
    const d = def("new", 6, 1, { minW: 6 });
    const placed = [item("a", 0, 0, 6, DASHBOARD_MAX_ROWS)];
    expect(disabledReasonFor(d, placed, 12)).toBeNull();
    expect(disabledReasonFor(d, placed, 6)).toBe(NO_ROOM);
  });
});

// --- Availability and permission (R3) -------------------------------------
//
// These two reasons differ from the three above in where they come from: the
// three above are facts about the layout being edited, these are facts about
// the cluster and the account. They are what stops the palette offering a
// cert-manager widget on a cluster with no cert-manager -- a row whose Add
// would succeed and then render an explicitly empty card forever.

/** A resolved family status carrying `detected`. */
function status(detected: unknown): SourceState {
  return {
    data: { detected },
    error: null,
    errorKind: null,
    loading: false,
    range: null,
  };
}

const statusForbidden: SourceState = {
  data: null,
  error: "Forbidden",
  errorKind: "permission",
  loading: false,
  range: null,
};

const statusLoading: SourceState = {
  data: null,
  error: null,
  errorKind: null,
  loading: true,
  range: null,
};

describe("disabledReasonFor -- family availability", () => {
  const certWidget = () =>
    def("expiring-certificates", 4, 4, { familyStatus: "certificates-status" });

  test("an entry whose feature is absent reports NOT_INSTALLED", () => {
    // Deliberately not on the dashboard: the widget-host path only fetches
    // what is placed, so this is the case R3 exists for.
    expect(
      disabledReasonFor(certWidget(), [], DASHBOARD_COLUMNS, {
        "certificates-status": status(false),
      }),
    ).toBe(NOT_INSTALLED);
  });

  test("an entry whose feature is present is addable", () => {
    expect(
      disabledReasonFor(certWidget(), [], DASHBOARD_COLUMNS, {
        "certificates-status": status(true),
      }),
    ).toBeNull();
  });

  test("a string family names its implementation rather than reporting absence", () => {
    const mesh = def("mesh-health", 4, 4, { familyStatus: "mesh-status" });
    expect(
      disabledReasonFor(mesh, [], DASHBOARD_COLUMNS, {
        "mesh-status": status("istio"),
      }),
    ).toBeNull();
    expect(
      disabledReasonFor(mesh, [], DASHBOARD_COLUMNS, {
        "mesh-status": status(""),
      }),
    ).toBe(NOT_INSTALLED);
  });

  test("an entry the account cannot read reports NOT_PERMITTED", () => {
    expect(
      disabledReasonFor(certWidget(), [], DASHBOARD_COLUMNS, {
        "certificates-status": statusForbidden,
      }),
    ).toBe(NOT_PERMITTED);
  });

  test("a status that has not answered yet blocks nothing", () => {
    // Refusing on a status still in flight would make the palette's contents
    // depend on how fast six discovery routes answer.
    expect(
      disabledReasonFor(certWidget(), [], DASHBOARD_COLUMNS, {
        "certificates-status": statusLoading,
      }),
    ).toBeNull();
    expect(
      disabledReasonFor(certWidget(), [], DASHBOARD_COLUMNS, {}),
    ).toBeNull();
  });

  test("a status that failed for some other reason blocks nothing", () => {
    // A transient 500 on a discovery route is not evidence the feature is
    // missing, and the widget's own error state covers it once it is added.
    expect(
      disabledReasonFor(certWidget(), [], DASHBOARD_COLUMNS, {
        "certificates-status": {
          data: null,
          error: "500",
          errorKind: "failure",
          loading: false,
          range: null,
        },
      }),
    ).toBeNull();
  });

  test("a widget declaring no family status ignores the statuses entirely", () => {
    expect(
      disabledReasonFor(def("nodes", 4, 4), [], DASHBOARD_COLUMNS, {
        "certificates-status": status(false),
        "mesh-status": statusForbidden,
      }),
    ).toBeNull();
  });

  test("unavailable beats already-placed", () => {
    // Both are true and only one can be shown. The cluster fact wins: it is
    // the one that says the card already on the dashboard cannot ever fill,
    // which is information the already-placed badge would hide.
    const d = certWidget();
    const placed = [item("a", 0, 0, 4, 4, "expiring-certificates")];
    expect(
      disabledReasonFor(d, placed, DASHBOARD_COLUMNS, {
        "certificates-status": status(false),
      }),
    ).toBe(NOT_INSTALLED);
  });

  test("not-permitted beats unavailable when the status was refused", () => {
    // A refused status carries no payload, so absence is not something we
    // know -- only that this account may not ask.
    expect(
      disabledReasonFor(certWidget(), [], DASHBOARD_COLUMNS, {
        "certificates-status": statusForbidden,
      }),
    ).toBe(NOT_PERMITTED);
  });

  test("the item cap still beats both", () => {
    const placed = fillerItems(DASHBOARD_MAX_ITEMS);
    expect(
      disabledReasonFor(certWidget(), placed, DASHBOARD_COLUMNS, {
        "certificates-status": status(false),
      }),
    ).toBe(DASHBOARD_FULL);
  });

  test("unavailable beats no-room", () => {
    const d = def(
      "expiring-certificates",
      DASHBOARD_COLUMNS,
      DASHBOARD_MAX_ROWS,
      {
        familyStatus: "certificates-status",
      },
    );
    const placed = [item("a", 0, 0, DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS)];
    expect(
      disabledReasonFor(d, placed, DASHBOARD_COLUMNS, {
        "certificates-status": status(false),
      }),
    ).toBe(NOT_INSTALLED);
  });
});

describe("disabledReasonFor -- an admin-gated entry", () => {
  // The one availability fact in this catalog that is NOT a family status.
  //
  // `/v1/clusters` and `/v1/audit/logs` are gated by `middleware.RequireAdmin`
  // rather than by RBAC, so the answer is a property of the SESSION and is
  // knowable without asking the cluster anything -- the roles on `/auth/me`
  // the shell has already loaded. There is no discovery route to declare and
  // nothing to fetch; without this, the palette would offer a row whose card
  // can only ever say "you do not have access" (R3).
  const auditWidget = () => def("audit-activity", 4, 4, { adminOnly: true });

  test("a non-admin is told before adding it, not after", () => {
    expect(
      disabledReasonFor(auditWidget(), [], DASHBOARD_COLUMNS, {}, false),
    ).toBe(NOT_PERMITTED);
  });

  test("an admin is offered it", () => {
    expect(
      disabledReasonFor(auditWidget(), [], DASHBOARD_COLUMNS, {}, true),
    ).toBeNull();
  });

  test("an unknown session blocks nothing", () => {
    // The same rule a family status in flight follows: the palette's contents
    // must not depend on whether `/auth/me` has answered yet, and the widget's
    // own permission state covers the case once it is added.
    expect(
      disabledReasonFor(auditWidget(), [], DASHBOARD_COLUMNS, {}, null),
    ).toBeNull();
    expect(
      disabledReasonFor(auditWidget(), [], DASHBOARD_COLUMNS, {}),
    ).toBeNull();
  });

  test("an ordinary widget is unaffected by a non-admin session", () => {
    expect(
      disabledReasonFor(
        def("pod-status", 4, 4),
        [],
        DASHBOARD_COLUMNS,
        {},
        false,
      ),
    ).toBeNull();
  });

  test("the refusal outranks a copy already on the dashboard", () => {
    // Same argument the family reasons carry: "not permitted" is what the user
    // needs to know about the card they already have, and "already on this
    // dashboard" hides it.
    const placed = [item("a", 0, 0, 4, 4, "audit-activity")];
    expect(
      disabledReasonFor(auditWidget(), placed, DASHBOARD_COLUMNS, {}, false),
    ).toBe(NOT_PERMITTED);
  });
});

describe("selectionAfterEntriesChange", () => {
  // The palette's rows become addable or not as discovery statuses and the
  // admin signal resolve, on their own schedule, while the dialog is open.
  // A selection left on a row that has since become unaddable looks selected
  // and silently swallows both Enter and a click, because `choose` no-ops on
  // a blocked entry and both paths go through it.
  test("a selection still pointing at an addable row is left alone", () => {
    // Not merely "does not crash": moving it would yank the user's cursor
    // every time an unrelated status landed.
    expect(selectionAfterEntriesChange([true, true, true], 2)).toBe(2);
    expect(selectionAfterEntriesChange([false, true, true], 1)).toBe(1);
  });

  test("a selection on a row that just became unaddable moves to the first addable one", () => {
    expect(selectionAfterEntriesChange([false, false, true], 0)).toBe(2);
    expect(selectionAfterEntriesChange([true, false, true], 1)).toBe(0);
  });

  test("nothing addable selects nothing, rather than promising a dead Enter", () => {
    expect(selectionAfterEntriesChange([false, false], 0)).toBe(-1);
    expect(selectionAfterEntriesChange([], 0)).toBe(-1);
  });

  test("an out-of-range selection is repaired, not preserved", () => {
    // The list can shrink underneath the selection as well as change shape.
    expect(selectionAfterEntriesChange([false, true], 7)).toBe(1);
    expect(selectionAfterEntriesChange([true, true], -1)).toBe(0);
  });
});
