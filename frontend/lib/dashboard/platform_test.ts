import { describe, expect, test } from "bun:test";
import {
  AUDIT_PAGE_HREF,
  auditActivityView,
  CLUSTER_STATES,
  CLUSTERS_PAGE_HREF,
  clusterStatusView,
  NOTIFICATIONS_PAGE_HREF,
  notificationsFeedView,
  PREFERENCES_START_HREF,
  pinnedResourcesView,
  savedViewsView,
} from "./platform.ts";

// The derived state behind the five platform widgets: `cluster-status`,
// `notifications-feed`, `audit-activity`, `saved-views` and
// `pinned-resources`.
//
// What these five share with the rest of lib/dashboard is totality: every
// argument may be null, missing or the wrong shape, and none of it throws. One
// unreadable item degrades to a counter rather than blanking a card describing
// the readable rest.
//
// What they do NOT share with the CRD-backed families is the shell's
// availability gate. None of these five is CRD-discovered, so none of them
// declares a family status, and an empty list here really does mean "nothing
// to report" -- with one exception that these functions have to carry
// themselves: an UNREADABLE body must never render as an empty one. "You have
// no pinned resources" and "this build could not read your pins" are opposite
// sentences, and only the first is reassuring.

// --------------------------------------------------------------------------
// cluster-status
// --------------------------------------------------------------------------

describe("clusterStatusView", () => {
  const CONNECTED = {
    id: "local",
    name: "local",
    status: "connected",
    isLocal: true,
    nodeCount: 3,
    k8sVersion: "v1.33.2",
  };
  const BROKEN = {
    id: "edge-2",
    name: "edge-2",
    status: "error",
    statusMessage: "dial tcp: i/o timeout",
    isLocal: false,
    nodeCount: 0,
  };

  test("an unreadable body is not an empty fleet", () => {
    // The two readings are opposite. A registry this build cannot parse must
    // not render as a registry with nothing in it -- the card would report a
    // fleet it could not read as a fleet with nothing wrong.
    for (const body of [null, undefined, "nope", 7, { data: [] }]) {
      const view = clusterStatusView(body, 5);
      expect(view.readable).toBe(false);
      expect(view.total).toBe(0);
      expect(view.rows).toEqual([]);
    }
  });

  test("an empty registry is readable and empty", () => {
    const view = clusterStatusView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.worst).toBeNull();
  });

  test("a single local cluster renders as one row", () => {
    // The scenario the acceptance list names: a deployment with a database and
    // no registered remotes still has the local cluster, seeded by
    // `EnsureLocal` at boot. A card that treated one row as "nothing to show"
    // would be empty on the majority of installs.
    const view = clusterStatusView([CONNECTED], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(1);
    expect(view.rows).toHaveLength(1);
    expect(view.rows[0].isLocal).toBe(true);
    expect(view.rows[0].state).toBe("connected");
    expect(view.worst).toBe("connected");
  });

  test("the worst state leads, and unhealthy clusters sort first", () => {
    const view = clusterStatusView([CONNECTED, BROKEN], 5);
    expect(view.worst).toBe("error");
    expect(view.rows.map((r) => r.id)).toEqual(["edge-2", "local"]);
    expect(view.counts.error).toBe(1);
    expect(view.counts.connected).toBe(1);
  });

  test("an unrecognised or missing status reads as unknown, never as connected", () => {
    // The safe direction: a status string this build does not know is not
    // evidence the cluster is reachable. Reporting it as connected is the one
    // error an operator will not check.
    const view = clusterStatusView(
      [
        { id: "a", name: "a", status: "" },
        { id: "b", name: "b" },
      ],
      5,
    );
    expect(view.counts.unknown).toBe(2);
    expect(view.counts.connected).toBe(0);
    expect(view.worst).toBe("unknown");
  });

  test("a row with no identity is counted, not rendered", () => {
    const view = clusterStatusView([CONNECTED, {}, "not an object"], 5);
    expect(view.unreadableRows).toBe(2);
    expect(view.rows).toHaveLength(1);
    expect(view.total).toBe(1);
  });

  test("rows are capped, and the cap does not touch the counts", () => {
    const many = Array.from({ length: 6 }, (_, i) => ({
      id: `c-${i}`,
      name: `c-${i}`,
      status: "connected",
    }));
    const view = clusterStatusView(many, 2);
    expect(view.rows).toHaveLength(2);
    expect(view.total).toBe(6);
    expect(view.counts.connected).toBe(6);
  });

  test("every declared state is a key of counts", () => {
    const view = clusterStatusView([], 5);
    const missing = CLUSTER_STATES.filter((s) => view.counts[s] === undefined);
    expect(missing).toEqual([]);
  });

  test("the page href is the settings route, not the stale /admin one", () => {
    // `/admin/clusters` appears in lib/notif-action.ts and does not exist in
    // this build; the page is `/settings/clusters`.
    expect(CLUSTERS_PAGE_HREF).toBe("/settings/clusters");
  });
});

// --------------------------------------------------------------------------
// notifications-feed
// --------------------------------------------------------------------------

describe("notificationsFeedView", () => {
  function feed(items: unknown, total?: unknown) {
    return total === undefined ? { items } : { items, total };
  }

  const CRIT = {
    id: "n1",
    severity: "critical",
    source: "alert",
    title: "etcd is down",
    message: "quorum lost",
    createdAt: "2026-09-20T10:00:00Z",
  };
  const INFO = {
    id: "n2",
    severity: "info",
    source: "gitops",
    title: "sync complete",
    message: "",
    createdAt: "2026-09-20T09:00:00Z",
  };

  test("an unreadable body is not an empty feed", () => {
    for (const body of [null, undefined, [], "nope", feed("not a list")]) {
      const view = notificationsFeedView(body, 5);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("an empty feed is readable and reports nothing unread", () => {
    const view = notificationsFeedView(feed([], 0), 5);
    expect(view.readable).toBe(true);
    expect(view.unread).toBe(0);
    expect(view.truncated).toBe(false);
  });

  test("unread items render worst-first with their severity counted", () => {
    const view = notificationsFeedView(feed([INFO, CRIT], 2), 5);
    expect(view.rows.map((r) => r.id)).toEqual(["n1", "n2"]);
    expect(view.counts.critical).toBe(1);
    expect(view.counts.info).toBe(1);
    expect(view.unread).toBe(2);
  });

  test("the server's total is the unread count, not the page length", () => {
    // The route caps a page; the count beside the rows has to be the whole
    // unread population or the badge under-reports on exactly the account
    // that most needs it.
    const view = notificationsFeedView(feed([CRIT, INFO], 42), 5);
    expect(view.unread).toBe(42);
    expect(view.truncated).toBe(true);
  });

  test("an unreadable total falls back to what the page carried", () => {
    const view = notificationsFeedView(feed([CRIT], "many"), 5);
    expect(view.unread).toBe(1);
    expect(view.truncated).toBe(false);
  });

  test("an item with no title is counted, not rendered", () => {
    const view = notificationsFeedView(feed([CRIT, {}, 3], 3), 5);
    expect(view.unreadableRows).toBe(2);
    expect(view.rows).toHaveLength(1);
  });

  test("an unrecognised severity is counted apart from the three known ones", () => {
    const view = notificationsFeedView(
      feed([{ id: "x", title: "t", severity: "spicy" }], 1),
      5,
    );
    expect(view.counts.other).toBe(1);
    expect(view.counts.critical).toBe(0);
  });

  test("the page href is the one every account can open", () => {
    // `/admin/notifications` is admin-only; the feed at `/notifications` is
    // not, and this card is offered to everyone.
    expect(NOTIFICATIONS_PAGE_HREF).toBe("/notifications");
  });
});

// --------------------------------------------------------------------------
// audit-activity
// --------------------------------------------------------------------------

describe("auditActivityView", () => {
  const OK = {
    timestamp: "2026-09-20T10:00:00Z",
    user: "chris",
    action: "update",
    result: "success",
    resourceKind: "deployments",
    resourceNamespace: "prod",
    resourceName: "api",
  };
  const DENIED = {
    timestamp: "2026-09-20T10:05:00Z",
    user: "intern",
    action: "delete",
    result: "denied",
    resourceKind: "secrets",
    resourceNamespace: "prod",
    resourceName: "db",
  };

  test("an unreadable body is not an empty log", () => {
    for (const body of [null, undefined, "nope", { data: [] }]) {
      const view = auditActivityView(body, 5);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("an empty log is readable and empty", () => {
    const view = auditActivityView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
  });

  test("recent activity renders in the order the route returned it", () => {
    // The handler orders by timestamp descending. Re-sorting here would fight
    // the audit page over the same rows.
    const view = auditActivityView([DENIED, OK], 5);
    expect(view.rows.map((r) => r.user)).toEqual(["intern", "chris"]);
    expect(view.rows[0].target).toBe("secrets/prod/db");
  });

  test("refused and failed writes are counted apart from successful ones", () => {
    const view = auditActivityView(
      [OK, DENIED, { ...OK, result: "failure" }],
      5,
    );
    expect(view.counts.success).toBe(1);
    expect(view.counts.denied).toBe(1);
    expect(view.counts.failure).toBe(1);
  });

  test("a cluster-scoped entry carries a target without a namespace", () => {
    const view = auditActivityView(
      [
        {
          ...OK,
          resourceKind: "nodes",
          resourceNamespace: "",
          resourceName: "n1",
        },
      ],
      5,
    );
    expect(view.rows[0].target).toBe("nodes/n1");
  });

  test("an entry naming no action is counted, not rendered", () => {
    const view = auditActivityView([OK, {}, null], 5);
    expect(view.unreadableRows).toBe(2);
    expect(view.rows).toHaveLength(1);
  });

  test("rows are capped and the counts are not", () => {
    const many = Array.from({ length: 8 }, () => OK);
    const view = auditActivityView(many, 3);
    expect(view.rows).toHaveLength(3);
    expect(view.total).toBe(8);
    expect(view.counts.success).toBe(8);
  });

  test("the page href is the settings route, not the stale /admin one", () => {
    expect(AUDIT_PAGE_HREF).toBe("/settings/audit");
  });
});

// --------------------------------------------------------------------------
// saved-views
// --------------------------------------------------------------------------

describe("savedViewsView", () => {
  const VIEW = {
    id: "v1",
    name: "failing pods",
    clusterId: "local",
    config: { resourceKind: "pods", namespace: "prod" },
  };

  test("an unreadable body is not an empty shelf", () => {
    for (const body of [null, undefined, "nope", { data: [] }]) {
      const view = savedViewsView(body, 5);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("no saved views is readable and empty", () => {
    const view = savedViewsView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.rows).toEqual([]);
  });

  test("a saved view links to the list page of the kind it scopes", () => {
    const view = savedViewsView([VIEW], 5);
    expect(view.rows[0].name).toBe("failing pods");
    expect(view.rows[0].kind).toBe("pods");
    expect(view.rows[0].href).toBe("/workloads/pods");
  });

  test("a view naming a kind this build cannot route renders without a link", () => {
    // Rather than a link that 404s. The row still says what the view scopes.
    const view = savedViewsView(
      [{ ...VIEW, config: { resourceKind: "widgets", namespace: "" } }],
      5,
    );
    expect(view.rows[0].href).toBeNull();
  });

  test("a record with no name is counted, not rendered", () => {
    const view = savedViewsView([VIEW, { id: "v2" }, 5], 5);
    expect(view.unreadableRows).toBe(2);
    expect(view.rows).toHaveLength(1);
  });

  test("views from every cluster are kept", () => {
    // Unlike a pin, a saved view describes a SCOPE -- a kind, a namespace, a
    // filter -- which another cluster could in principle satisfy. The rule is
    // pin-store.ts's, verbatim.
    const view = savedViewsView(
      [VIEW, { ...VIEW, id: "v2", clusterId: "edge" }],
      5,
    );
    expect(view.total).toBe(2);
  });

  test("the empty state points somewhere a view can be made", () => {
    expect(PREFERENCES_START_HREF).toBe("/workloads/pods");
  });
});

// --------------------------------------------------------------------------
// pinned-resources
// --------------------------------------------------------------------------

describe("pinnedResourcesView", () => {
  const PIN = {
    id: "p1",
    name: "api",
    clusterId: "local",
    config: {
      resourceKind: "deployments",
      namespace: "prod",
      name: "api",
      displayKind: "Deployment",
    },
  };
  const ELSEWHERE = { ...PIN, id: "p2", clusterId: "edge-2" };

  test("an unreadable body is not an empty board", () => {
    for (const body of [null, undefined, "nope", { data: [] }]) {
      const view = pinnedResourcesView(body, "local", 5);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("no pins is readable and empty", () => {
    const view = pinnedResourcesView([], "local", 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.otherClusters).toBe(0);
  });

  test("a pin links to the object it names", () => {
    const view = pinnedResourcesView([PIN], "local", 5);
    expect(view.rows[0].href).toBe("/workloads/deployments/prod/api");
    expect(view.rows[0].kind).toBe("Deployment");
  });

  test("pins on other clusters are withheld and reported, not shown", () => {
    // A pin names one object in one cluster, so a pin from another cluster
    // cannot be opened from here (pin-store.ts). Counting them is what keeps
    // "you have no pins" from being said to someone with twenty.
    const view = pinnedResourcesView([PIN, ELSEWHERE], "local", 5);
    expect(view.total).toBe(1);
    expect(view.otherClusters).toBe(1);
    expect(view.rows.map((r) => r.id)).toEqual(["p1"]);
  });

  test("a pin naming no object is counted, not rendered", () => {
    const view = pinnedResourcesView(
      [PIN, { id: "p3", clusterId: "local" }, 9],
      "local",
      5,
    );
    expect(view.unreadableRows).toBe(2);
    expect(view.rows).toHaveLength(1);
  });

  test("a pin the active cluster cannot be told apart from is still withheld", () => {
    // A record carrying no cluster is not a record belonging to this one.
    const view = pinnedResourcesView(
      [{ ...PIN, clusterId: undefined }],
      "local",
      5,
    );
    expect(view.total).toBe(0);
    expect(view.otherClusters).toBe(1);
  });

  test("rows are capped and the count is not", () => {
    const many = Array.from({ length: 7 }, (_, i) => ({
      ...PIN,
      id: `p-${i}`,
    }));
    const view = pinnedResourcesView(many, "local", 3);
    expect(view.rows).toHaveLength(3);
    expect(view.total).toBe(7);
  });
});
