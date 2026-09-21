import { describe, expect, test } from "bun:test";
import {
  APP_HEALTH_BUCKETS,
  classifyApp,
  coveragePercent,
  GITOPS_APPLICATIONS_PAGE_HREF,
  gitopsAppHealthView,
  gitopsAppHref,
  gitopsRecentSyncsView,
  MESH_MTLS_PAGE_HREF,
  mtlsCoverageView,
  shortRevision,
} from "./sync-state.ts";

// The derived state behind `gitops-app-health`, `gitops-recent-syncs` and
// `mtls-coverage`.
//
// Three payloads, one shared habit: every number a card prints has to be
// attributable to one application or one workload, counted once. The GitOps
// list route reports sync and health as two INDEPENDENT fields, and the
// server's own summary counts them in two independent switches -- so an
// application that is both out of sync and degraded appears in `outOfSync`
// AND in `degraded`, and a card adding those two up over-reports. Collapsing
// the pair into one bucket per application is the judgement this module makes
// and the reason it is not a field read (D-10, KTD8).
//
// What is NOT here is availability. Whether Argo CD, Flux or a service mesh is
// installed is answered by that family's discovery route and resolved by the
// shell before `render` is called (KTD1, R1), so an empty list at this point
// means "nothing to report" and may say so.

// --------------------------------------------------------------------------
// Fixtures
// --------------------------------------------------------------------------

function app(over: Record<string, unknown> = {}) {
  return {
    id: "argo:argocd:api",
    name: "api",
    namespace: "argocd",
    tool: "argocd",
    kind: "Application",
    syncStatus: "synced",
    healthStatus: "healthy",
    source: {},
    managedResourceCount: 3,
    suspended: false,
    ...over,
  };
}

/** The route's envelope. `applications` is a Go slice, so it marshals as
 * `null` -- not `[]` -- when nothing survives the RBAC filter. */
function apps(list: unknown, summary: unknown = null) {
  return { applications: list, summary };
}

function workload(over: Record<string, unknown> = {}) {
  return {
    namespace: "prod",
    workload: "api",
    workloadKind: "Deployment",
    mesh: "istio",
    state: "active",
    source: "policy",
    workloadKindConfident: true,
    ...over,
  };
}

function posture(list: unknown, errors?: unknown) {
  return {
    status: { detected: "istio", lastChecked: "" },
    workloads: list,
    ...(errors === undefined ? {} : { errors }),
  };
}

// --------------------------------------------------------------------------
// classifyApp
// --------------------------------------------------------------------------

describe("classifyApp", () => {
  test("a synced, healthy application is synced", () => {
    expect(classifyApp(app())).toBe("synced");
  });

  test("an application that is both out of sync and degraded counts as degraded", () => {
    // The scenario this module exists for. The server's `computeMetadata`
    // increments `outOfSync` and `degraded` in two separate switches, so this
    // application is in both of its counters; a card summing them reports two
    // problems where there is one application. Degraded outranks out-of-sync
    // because it describes what is RUNNING: a workload that is live and broken
    // is worse news than one whose manifest has drifted from git.
    expect(
      classifyApp(app({ syncStatus: "outofsync", healthStatus: "degraded" })),
    ).toBe("degraded");
  });

  test("the three sync statuses the server folds into out-of-sync fold here too", () => {
    // `computeMetadata` counts outofsync, failed and stalled in one bucket. A
    // card that split them would disagree with the applications page about the
    // same cluster.
    for (const s of ["outofsync", "failed", "stalled"]) {
      expect(classifyApp(app({ syncStatus: s }))).toBe("outofsync");
    }
  });

  test("suspended outranks synced but not a real problem", () => {
    // A suspended application is not reconciling, so calling it synced would
    // claim git and the cluster agree when nothing is checking. It is still
    // not a fault, so it ranks below both problem buckets.
    expect(classifyApp(app({ healthStatus: "suspended" }))).toBe("suspended");
    expect(
      classifyApp(app({ syncStatus: "outofsync", healthStatus: "suspended" })),
    ).toBe("outofsync");
  });

  test("progressing is reported as progressing from either field", () => {
    expect(classifyApp(app({ syncStatus: "progressing" }))).toBe("progressing");
    expect(classifyApp(app({ healthStatus: "progressing" }))).toBe(
      "progressing",
    );
  });

  test("an unrecognised or missing status is unknown, never synced", () => {
    // Rounding an unreadable state toward healthy is the same defect class as
    // rendering an absent operator as good news.
    expect(
      classifyApp(app({ syncStatus: "unknown", healthStatus: "unknown" })),
    ).toBe("unknown");
    expect(classifyApp(app({ syncStatus: "", healthStatus: "" }))).toBe(
      "unknown",
    );
    expect(classifyApp({})).toBe("unknown");
    expect(classifyApp(null)).toBe("unknown");
  });

  test("every bucket it can return is declared", () => {
    const seen = new Set([
      classifyApp(app()),
      classifyApp(app({ healthStatus: "degraded" })),
      classifyApp(app({ syncStatus: "outofsync" })),
      classifyApp(app({ syncStatus: "progressing" })),
      classifyApp(app({ healthStatus: "suspended" })),
      classifyApp({}),
    ]);
    for (const b of seen) expect(APP_HEALTH_BUCKETS).toContain(b);
  });
});

// --------------------------------------------------------------------------
// gitopsAppHealthView
// --------------------------------------------------------------------------

describe("gitopsAppHealthView", () => {
  test("an empty payload does not throw and reports nothing to show", () => {
    const view = gitopsAppHealthView(apps([]), 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.rows).toEqual([]);
    expect(view.attention).toBe(0);
    expect(view.tools).toEqual([]);
  });

  test("a null applications field reads as empty, not as unreadable", () => {
    // The route builds its result with `var out []NormalizedApp`, so a cluster
    // whose applications the caller cannot see serialises `"applications":
    // null`. That is zero applications, and a card that called it unreadable
    // would tell an operator their GitOps install is broken when it is merely
    // empty.
    const view = gitopsAppHealthView(apps(null), 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
  });

  test("a body that is not the route's envelope reads as unreadable", () => {
    // Distinct from empty, and it has to stay that way: an unreadable body
    // rendered as an empty one is a card reporting a healthy fleet it never
    // saw.
    for (const bad of [null, undefined, 42, "nope", [], { applications: 7 }]) {
      expect(gitopsAppHealthView(bad, 5).readable).toBe(false);
    }
  });

  test("applications from both tools roll up together and stay attributable", () => {
    const view = gitopsAppHealthView(
      apps([
        app({ id: "argo:argocd:api", name: "api", tool: "argocd" }),
        app({
          id: "flux-ks:flux-system:infra",
          name: "infra",
          namespace: "flux-system",
          tool: "fluxcd",
          kind: "Kustomization",
          syncStatus: "outofsync",
        }),
        app({
          id: "flux-hr:flux-system:redis",
          name: "redis",
          namespace: "flux-system",
          tool: "fluxcd",
          kind: "HelmRelease",
          healthStatus: "degraded",
        }),
      ]),
      5,
    );

    expect(view.total).toBe(3);
    expect(view.counts.synced).toBe(1);
    expect(view.counts.outofsync).toBe(1);
    expect(view.counts.degraded).toBe(1);
    // Per-tool totals, so the card can say the roll-up spans both without
    // splitting the fleet into two cards.
    expect(view.tools).toEqual([
      { tool: "argocd", total: 1 },
      { tool: "fluxcd", total: 2 },
    ]);
    // Every row carries its own tool, which is what lets the rendering badge
    // each one.
    expect(view.rows.map((r) => [r.name, r.tool, r.bucket])).toEqual([
      ["redis", "fluxcd", "degraded"],
      ["infra", "fluxcd", "outofsync"],
    ]);
  });

  test("an application in two problem states is counted once", () => {
    const view = gitopsAppHealthView(
      apps([app({ syncStatus: "outofsync", healthStatus: "degraded" })]),
      5,
    );
    expect(view.total).toBe(1);
    expect(view.counts.degraded).toBe(1);
    expect(view.counts.outofsync).toBe(0);
    // The counts partition the fleet: they sum to the total, which is the
    // property the server's own summary does not have.
    const summed = APP_HEALTH_BUCKETS.reduce((n, b) => n + view.counts[b], 0);
    expect(summed).toBe(view.total);
    expect(view.attention).toBe(1);
  });

  test("rows are worst-first, capped, and the card can say how many it hid", () => {
    const view = gitopsAppHealthView(
      apps([
        app({ id: "a:n:z", name: "z", syncStatus: "outofsync" }),
        app({ id: "a:n:y", name: "y", healthStatus: "degraded" }),
        app({ id: "a:n:x", name: "x", syncStatus: "failed" }),
        app({ id: "a:n:w", name: "w" }),
      ]),
      2,
    );
    expect(view.rows.map((r) => r.name)).toEqual(["y", "x"]);
    expect(view.attention).toBe(3);
  });

  test("only applications needing attention become rows", () => {
    // A healthy fleet has no rows and the card says so in words, rather than
    // listing four green lines the operator has to read to learn nothing.
    const view = gitopsAppHealthView(apps([app(), app({ id: "a:n:b" })]), 5);
    expect(view.rows).toEqual([]);
    expect(view.attention).toBe(0);
    expect(view.counts.synced).toBe(2);
  });

  test("an entry naming no application is counted, not rendered", () => {
    const view = gitopsAppHealthView(
      apps([app({ syncStatus: "outofsync" }), { name: "" }, 7, null]),
      5,
    );
    expect(view.total).toBe(1);
    expect(view.unreadableRows).toBe(3);
    expect(view.rows).toHaveLength(1);
  });

  test("a limit of zero or less yields no rows rather than throwing", () => {
    const payload = apps([app({ syncStatus: "outofsync" })]);
    expect(gitopsAppHealthView(payload, 0).rows).toEqual([]);
    expect(gitopsAppHealthView(payload, -3).rows).toEqual([]);
  });
});

// --------------------------------------------------------------------------
// shortRevision
// --------------------------------------------------------------------------

describe("shortRevision", () => {
  test("a commit sha is shortened to seven characters", () => {
    expect(shortRevision("0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a")).toBe(
      "0f1a2b3",
    );
  });

  test("a Flux revision keeps its branch and shortens only the sha", () => {
    // Flux writes `main@sha1:0f1a2b3c...`, and truncating the whole string to
    // seven characters would leave "main@sh".
    expect(
      shortRevision("main@sha1:0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a"),
    ).toBe("main@0f1a2b3");
  });

  test("a tag or chart version is left alone", () => {
    expect(shortRevision("v1.4.2")).toBe("v1.4.2");
    expect(shortRevision("18.1.5")).toBe("18.1.5");
  });

  test("an unreadable revision is the empty string", () => {
    expect(shortRevision(undefined)).toBe("");
    expect(shortRevision(null)).toBe("");
    expect(shortRevision(42)).toBe("");
    expect(shortRevision("   ")).toBe("");
  });
});

// --------------------------------------------------------------------------
// gitopsRecentSyncsView
// --------------------------------------------------------------------------

describe("gitopsRecentSyncsView", () => {
  test("an empty payload does not throw", () => {
    const view = gitopsRecentSyncsView(apps([]), 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.rows).toEqual([]);
    expect(view.untimed).toBe(0);
  });

  test("a null applications field reads as empty, not as unreadable", () => {
    expect(gitopsRecentSyncsView(apps(null), 5).readable).toBe(true);
    expect(gitopsRecentSyncsView(apps(null), 5).total).toBe(0);
  });

  test("a body that is not the route's envelope reads as unreadable", () => {
    for (const bad of [null, undefined, "nope", [], { applications: 7 }]) {
      expect(gitopsRecentSyncsView(bad, 5).readable).toBe(false);
    }
  });

  test("the card renders fully with no commit enrichment", () => {
    // `/v1/gitops/commits` needs a repository URL AND a set of shas, and
    // answers with a neutral empty shape when no Git provider token is
    // configured -- so it cannot be this card's data source. Everything a row
    // shows comes off the applications payload: who synced, when, and to
    // which revision.
    const view = gitopsRecentSyncsView(
      apps([
        app({
          id: "argo:argocd:api",
          name: "api",
          lastSyncTime: "2026-09-20T12:00:00Z",
          currentRevision: "0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a",
        }),
      ]),
      5,
    );
    expect(view.rows).toHaveLength(1);
    const row = view.rows[0];
    expect(row.name).toBe("api");
    expect(row.tool).toBe("argocd");
    expect(row.revision).toBe("0f1a2b3");
    expect(row.syncedAt).toBe("2026-09-20T12:00:00Z");
    expect(row.syncedAtMs).toBe(Date.parse("2026-09-20T12:00:00Z"));
    expect(row.href).toBe(gitopsAppHref("argo:argocd:api"));
  });

  test("a row with no revision still renders; the field is simply empty", () => {
    const view = gitopsRecentSyncsView(
      apps([app({ lastSyncTime: "2026-09-20T12:00:00Z" })]),
      5,
    );
    expect(view.rows).toHaveLength(1);
    expect(view.rows[0].revision).toBe("");
  });

  test("rows are newest first and capped at the limit", () => {
    const view = gitopsRecentSyncsView(
      apps([
        app({
          id: "a:n:old",
          name: "old",
          lastSyncTime: "2026-09-18T00:00:00Z",
        }),
        app({
          id: "a:n:new",
          name: "new",
          lastSyncTime: "2026-09-20T00:00:00Z",
        }),
        app({
          id: "a:n:mid",
          name: "mid",
          lastSyncTime: "2026-09-19T00:00:00Z",
        }),
      ]),
      2,
    );
    expect(view.rows.map((r) => r.name)).toEqual(["new", "mid"]);
    expect(view.total).toBe(3);
  });

  test("an application with no recorded sync time is counted, not ranked", () => {
    // Placing an application that has never synced at the top of a
    // "recent syncs" list -- or at the bottom, dated the epoch -- would both
    // be inventions. It is reported as a count instead.
    const view = gitopsRecentSyncsView(
      apps([
        app({ id: "a:n:never", name: "never" }),
        app({
          id: "a:n:once",
          name: "once",
          lastSyncTime: "2026-09-20T00:00:00Z",
        }),
        app({ id: "a:n:bad", name: "bad", lastSyncTime: "not-a-date" }),
      ]),
      5,
    );
    expect(view.rows.map((r) => r.name)).toEqual(["once"]);
    expect(view.untimed).toBe(2);
    expect(view.total).toBe(3);
  });

  test("an entry naming no application is counted, not rendered", () => {
    const view = gitopsRecentSyncsView(
      apps([app({ lastSyncTime: "2026-09-20T00:00:00Z" }), null, { name: "" }]),
      5,
    );
    expect(view.total).toBe(1);
    expect(view.unreadableRows).toBe(2);
  });
});

// --------------------------------------------------------------------------
// coveragePercent
// --------------------------------------------------------------------------

describe("coveragePercent", () => {
  test("the two absolutes are exact", () => {
    expect(coveragePercent(0)).toBe(0);
    expect(coveragePercent(1)).toBe(100);
  });

  test("a fraction that rounds to zero is reported as one percent", () => {
    // One strict workload in three hundred is 0.33%, and Math.round makes that
    // 0 -- a card claiming NO workload enforces strict mTLS while one does.
    // The floor is the smaller lie, and it is in the direction that keeps the
    // card honest about the absolute zero above it.
    expect(coveragePercent(1 / 300)).toBe(1);
  });

  test("a fraction that rounds to a hundred is reported as ninety-nine", () => {
    // 299 of 300 is 99.67%, and a card printing "100%" about a cluster with a
    // permissive workload in it is the reassuring falsehood this release
    // exists to prevent.
    expect(coveragePercent(299 / 300)).toBe(99);
  });

  test("an ordinary fraction rounds", () => {
    expect(coveragePercent(0.5)).toBe(50);
    expect(coveragePercent(2 / 3)).toBe(67);
  });

  test("null and out-of-range values stay unreportable", () => {
    expect(coveragePercent(null)).toBeNull();
    expect(coveragePercent(Number.NaN)).toBeNull();
  });
});

// --------------------------------------------------------------------------
// mtlsCoverageView
// --------------------------------------------------------------------------

describe("mtlsCoverageView", () => {
  test("an empty payload does not throw and reports no meshed workloads", () => {
    const view = mtlsCoverageView(posture([]), 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.meshed).toBe(0);
    // Null, not zero. No meshed workload is not the same as no meshed workload
    // enforcing strict mTLS, and the card says different things about the two.
    expect(view.coverage).toBeNull();
    expect(view.percent).toBeNull();
    expect(view.rows).toEqual([]);
  });

  test("a null workloads field reads as empty, not as unreadable", () => {
    const view = mtlsCoverageView(posture(null), 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
  });

  test("a body that is not the route's envelope reads as unreadable", () => {
    for (const bad of [null, undefined, 42, "nope", [], { workloads: 7 }]) {
      expect(mtlsCoverageView(bad, 5).readable).toBe(false);
    }
  });

  test("mixed strict and permissive workloads render the proportion", () => {
    const view = mtlsCoverageView(
      posture([
        workload({ workload: "a", state: "active" }),
        workload({ workload: "b", state: "active" }),
        workload({ workload: "c", state: "mixed" }),
        workload({ workload: "d", state: "inactive" }),
      ]),
      5,
    );
    expect(view.total).toBe(4);
    expect(view.meshed).toBe(4);
    expect(view.counts.active).toBe(2);
    expect(view.counts.mixed).toBe(1);
    expect(view.counts.inactive).toBe(1);
    expect(view.coverage).toBe(0.5);
    expect(view.percent).toBe(50);
  });

  test("no strict workload renders zero coverage, not an empty card", () => {
    const view = mtlsCoverageView(
      posture([
        workload({ workload: "a", state: "inactive" }),
        workload({ workload: "b", state: "mixed" }),
      ]),
      5,
    );
    expect(view.meshed).toBe(2);
    expect(view.coverage).toBe(0);
    expect(view.percent).toBe(0);
    // And the two workloads keeping it at zero are the rows worth showing.
    expect(view.rows.map((r) => [r.workload, r.state])).toEqual([
      ["a", "inactive"],
      ["b", "mixed"],
    ]);
  });

  test("unmeshed workloads are reported apart rather than counted against coverage", () => {
    // `unmeshed` is an opt-out, not a failure -- a workload outside the mesh
    // has no mTLS posture to enforce. Folding it into the denominator would
    // make a cluster look worse the more it deliberately excluded, and folding
    // it into the numerator would be worse still.
    const view = mtlsCoverageView(
      posture([
        workload({ workload: "a", state: "active" }),
        workload({ workload: "b", state: "unmeshed", mesh: "" }),
        workload({ workload: "c", state: "unmeshed", mesh: "" }),
      ]),
      5,
    );
    expect(view.total).toBe(3);
    expect(view.meshed).toBe(1);
    expect(view.counts.unmeshed).toBe(2);
    expect(view.coverage).toBe(1);
    expect(view.percent).toBe(100);
    expect(view.rows).toEqual([]);
  });

  test("rows are worst-first and capped", () => {
    const view = mtlsCoverageView(
      posture([
        workload({ namespace: "b", workload: "m", state: "mixed" }),
        workload({ namespace: "a", workload: "i", state: "inactive" }),
        workload({ namespace: "c", workload: "j", state: "inactive" }),
        workload({ namespace: "d", workload: "n", state: "mixed" }),
      ]),
      3,
    );
    expect(view.rows.map((r) => `${r.namespace}/${r.workload}`)).toEqual([
      "a/i",
      "c/j",
      "b/m",
    ]);
  });

  test("an entry naming no workload is counted, not rendered", () => {
    const view = mtlsCoverageView(
      posture([workload({ state: "inactive" }), null, { namespace: "x" }, 9]),
      5,
    );
    expect(view.total).toBe(1);
    expect(view.unreadableRows).toBe(3);
  });

  test("an unrecognised state is counted apart from every posture", () => {
    // Not rounded into `active`, which would report an unreadable posture as
    // an enforced one, and not into `inactive`, which would raise an alarm
    // about something this build simply does not recognise.
    const view = mtlsCoverageView(
      posture([workload({ state: "sideways" }), workload({ workload: "b" })]),
      5,
    );
    expect(view.total).toBe(2);
    expect(view.counts.unknown).toBe(1);
    expect(view.meshed).toBe(1);
    expect(view.coverage).toBe(1);
  });

  test("the route's partial-failure keys are surfaced, sorted", () => {
    // The pod list is capped and the Prometheus cross-check can fail, and both
    // leave a posture table that is real but incomplete. A coverage percentage
    // computed over a truncated list and printed without that caveat is the
    // card's own version of absence-as-good-news.
    const view = mtlsCoverageView(
      posture([workload()], {
        truncated: "result capped at 500 pods",
        "prometheus-cross-check": "metric cross-check unavailable",
      }),
      5,
    );
    expect(view.partial).toEqual(["prometheus-cross-check", "truncated"]);
  });

  test("no partial-failure keys is an empty list, not a missing one", () => {
    expect(mtlsCoverageView(posture([workload()]), 5).partial).toEqual([]);
  });

  test("a limit of zero or less yields no rows rather than throwing", () => {
    const payload = posture([workload({ state: "inactive" })]);
    expect(mtlsCoverageView(payload, 0).rows).toEqual([]);
    expect(mtlsCoverageView(payload, -3).rows).toEqual([]);
  });
});

// --------------------------------------------------------------------------
// Links
// --------------------------------------------------------------------------

describe("full-page links", () => {
  test("the two page hrefs are the routes the pages are actually mounted at", () => {
    expect(GITOPS_APPLICATIONS_PAGE_HREF).toBe("/gitops/applications");
    expect(MESH_MTLS_PAGE_HREF).toBe("/networking/mesh/mtls");
  });

  test("a composite id is encoded into the detail href", () => {
    // The id carries colons by construction (`tool:namespace:name`), and it is
    // a path segment here. Leaving it raw would work today and break the first
    // time a name arrives that does not.
    expect(gitopsAppHref("flux-hr:flux-system:redis")).toBe(
      "/gitops/applications/flux-hr%3Aflux-system%3Aredis",
    );
  });
});
