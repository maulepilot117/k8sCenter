import { describe, expect, test } from "bun:test";
import {
  BACKUPS_PAGE_HREF,
  backupsView,
  CERTIFICATES_PAGE_HREF,
  certificateHref,
  certsExpiringView,
  classifyExpiry,
  ESO_DRIFT_STATES,
  EXTERNAL_SECRETS_PAGE_HREF,
  esoHealthView,
  externalSecretHref,
  SNAPSHOTS_PAGE_HREF,
  snapshotHealthView,
} from "./expiry.ts";

// The derived state behind `certs-expiring`, `eso-health`, `velero-backups`
// and `snapshot-health` -- the certificates, secrets and backups family.
//
// What these four share is a roll-up that has to rank by badness rather than
// count by category: a failed backup outranks a running one, a critical
// certificate outranks a merely warning one, and a card that sorted by
// recency or by name would bury the one row the operator opened it for.
//
// What they do NOT share is a definition of "soon". The certificate half of
// this module classifies against thresholds the SERVER resolved per
// certificate -- walking certificate to issuer to cluster-issuer, and falling
// back to its own defaults when the resolved pair contradicts itself. Those
// numbers arrive on the payload and are read, never recomputed (KTD8, and the
// annotation contract in CLAUDE.md). The first describe block below is the pin
// on that.
//
// Absence is the shell's business throughout: a family that is not installed
// never reaches these functions, because `resolveWidgetState` returns
// `unavailable` before `render` is called (KTD1, R1). That is what lets an
// empty list here mean "nothing to report" and say so plainly.

// --------------------------------------------------------------------------
// Expiry classification
// --------------------------------------------------------------------------

describe("classifyExpiry", () => {
  test("splits at the payload's own boundaries, inclusive at both", () => {
    // These are the backend's boundaries verbatim: HandleListExpiring skips a
    // certificate when `*days > warn`, calls the rest "warning", and promotes
    // to "critical" when `*days <= crit`. Both comparisons are inclusive, and
    // a card that used `<` on either edge would disagree with the certificates
    // page about the same certificate on the same day.
    expect(classifyExpiry(6, 30, 7)).toBe("critical");
    expect(classifyExpiry(7, 30, 7)).toBe("critical");
    expect(classifyExpiry(8, 30, 7)).toBe("warning");
    expect(classifyExpiry(30, 30, 7)).toBe("warning");
    expect(classifyExpiry(31, 30, 7)).toBe("healthy");
  });

  test("an already-expired certificate is critical, not an arithmetic accident", () => {
    expect(classifyExpiry(0, 30, 7)).toBe("critical");
    expect(classifyExpiry(-12, 30, 7)).toBe("critical");
  });

  test("classifies against the thresholds it is given, not against defaults", () => {
    // The whole point. 25 days is comfortably healthy under cert-manager's
    // 30/7 defaults and is CRITICAL under an operator's 60/30 annotation
    // chain. A browser-side recompute would get this wrong in a way nobody
    // would notice until a certificate expired.
    expect(classifyExpiry(25, 60, 30)).toBe("critical");
    expect(classifyExpiry(45, 60, 30)).toBe("warning");
    expect(classifyExpiry(61, 60, 30)).toBe("healthy");
  });

  test("an unreadable day count or threshold classifies as nothing at all", () => {
    // Null rather than "healthy". A certificate whose expiry we could not read
    // is not a certificate that is fine, and folding it into the healthy
    // bucket is the same defect class as rendering an absent operator as good
    // news.
    expect(classifyExpiry(undefined, 30, 7)).toBeNull();
    expect(classifyExpiry(null, 30, 7)).toBeNull();
    expect(classifyExpiry("10", 30, 7)).toBeNull();
    expect(classifyExpiry(Number.NaN, 30, 7)).toBeNull();
    expect(classifyExpiry(10, undefined, 7)).toBeNull();
    expect(classifyExpiry(10, 30, undefined)).toBeNull();
    expect(classifyExpiry(10, 0, 7)).toBeNull();
  });
});

// --------------------------------------------------------------------------
// certs-expiring
// --------------------------------------------------------------------------

describe("certsExpiringView", () => {
  function cert(over: Record<string, unknown> = {}) {
    return {
      namespace: "apps",
      name: "web-tls",
      daysRemaining: 12,
      warningThresholdDays: 30,
      criticalThresholdDays: 7,
      ...over,
    };
  }

  test("an empty payload is readable and says nothing is expiring", () => {
    const view = certsExpiringView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.expiring).toBe(0);
    expect(view.rows).toEqual([]);
    expect(view.unreadableRows).toBe(0);
  });

  test("a payload this build cannot read is not an empty one", () => {
    for (const bad of [null, undefined, 42, "certs", { items: [] }]) {
      const view = certsExpiringView(bad, 5);
      expect(view.readable).toBe(false);
      expect(view.total).toBe(0);
      expect(view.rows).toEqual([]);
    }
  });

  test("counts split critical, warning and healthy at each certificate's own thresholds", () => {
    const view = certsExpiringView(
      [
        cert({ name: "a", daysRemaining: 3 }),
        cert({ name: "b", daysRemaining: 7 }),
        cert({ name: "c", daysRemaining: 8 }),
        cert({ name: "d", daysRemaining: 30 }),
        cert({ name: "e", daysRemaining: 31 }),
        // Its own chain resolved 60/30, so 25 days is critical HERE and
        // healthy for every other row above.
        cert({
          name: "f",
          daysRemaining: 25,
          warningThresholdDays: 60,
          criticalThresholdDays: 30,
        }),
      ],
      10,
    );
    expect(view.total).toBe(6);
    expect(view.critical).toBe(3);
    expect(view.warning).toBe(2);
    expect(view.healthy).toBe(1);
    expect(view.expiring).toBe(5);
  });

  test("a threshold-conflicted certificate renders under the classification the server resolved", () => {
    // The annotation contract: when the resolved critical threshold is not
    // stricter than the resolved warning one, the SERVER falls back to its own
    // defaults and marks the certificate `thresholdConflict: true` -- see
    // `resolveCertThresholdsDetailed`, which returns
    // (WarningThresholdDays, CriticalThresholdDays, default, default, true).
    //
    // So the payload for a conflicted certificate already carries 30/7, and a
    // card that honoured the operator's annotation instead -- or that tried to
    // "fix" the conflict itself -- would contradict both the server and the
    // certificates page. This pins that the flag changes nothing about the
    // classification and is only surfaced as an explanation.
    const view = certsExpiringView(
      [
        cert({
          name: "conflicted",
          daysRemaining: 20,
          warningThresholdDays: 30,
          criticalThresholdDays: 7,
          thresholdConflict: true,
        }),
      ],
      5,
    );
    expect(view.warning).toBe(1);
    expect(view.critical).toBe(0);
    expect(view.conflicted).toBe(1);
    expect(view.rows[0].class).toBe("warning");
    expect(view.rows[0].thresholdConflict).toBe(true);
    expect(view.rows[0].warnDays).toBe(30);
    expect(view.rows[0].criticalDays).toBe(7);
  });

  test("a certificate is never silently re-derived from the default thresholds", () => {
    // The mirror of the test above, and the one that would fail if anyone ever
    // hard-coded 30/7 in the browser: a certificate whose server-resolved
    // thresholds are 90/45 is critical at 40 days, which no default
    // classification can produce.
    const view = certsExpiringView(
      [
        cert({
          name: "long-lived",
          daysRemaining: 40,
          warningThresholdDays: 90,
          criticalThresholdDays: 45,
        }),
      ],
      5,
    );
    expect(view.critical).toBe(1);
    expect(view.healthy).toBe(0);
    expect(view.rows[0].class).toBe("critical");
  });

  test("rows rank critical before warning and soonest first inside each class", () => {
    const view = certsExpiringView(
      [
        cert({ name: "warn-late", daysRemaining: 29 }),
        cert({ name: "crit-late", daysRemaining: 6 }),
        cert({ name: "warn-soon", daysRemaining: 9 }),
        cert({ name: "crit-soon", daysRemaining: 1 }),
        cert({ name: "fine", daysRemaining: 300 }),
      ],
      10,
    );
    expect(view.rows.map((r) => r.name)).toEqual([
      "crit-soon",
      "crit-late",
      "warn-soon",
      "warn-late",
    ]);
  });

  test("healthy certificates never appear as rows, however many there are", () => {
    const view = certsExpiringView(
      [
        cert({ name: "a", daysRemaining: 200 }),
        cert({ name: "b", daysRemaining: 900 }),
      ],
      5,
    );
    expect(view.rows).toEqual([]);
    expect(view.healthy).toBe(2);
    expect(view.expiring).toBe(0);
  });

  test("the row list honours the caller's cap while the counts do not", () => {
    const certs = Array.from({ length: 9 }, (_, i) =>
      cert({ name: `c${i}`, daysRemaining: i + 1 }),
    );
    const view = certsExpiringView(certs, 4);
    expect(view.rows).toHaveLength(4);
    expect(view.critical + view.warning).toBe(9);
  });

  test("an unclassifiable certificate is counted apart, never as healthy", () => {
    const view = certsExpiringView(
      [
        cert({ name: "no-days", daysRemaining: undefined }),
        cert({ name: "no-thresholds", warningThresholdDays: undefined }),
        cert({ name: "fine", daysRemaining: 400 }),
      ],
      5,
    );
    expect(view.unclassified).toBe(2);
    expect(view.healthy).toBe(1);
    expect(view.total).toBe(3);
  });

  test("an entry naming no certificate is counted rather than rendered", () => {
    const view = certsExpiringView([cert(), { namespace: "apps" }, null, 7], 5);
    expect(view.unreadableRows).toBe(3);
    expect(view.total).toBe(1);
    expect(view.rows).toHaveLength(1);
  });

  test("the full page and per-certificate links are encoded", () => {
    expect(CERTIFICATES_PAGE_HREF).toBe("/security/certificates");
    expect(certificateHref("apps", "web-tls")).toBe(
      "/security/certificates/apps/web-tls",
    );
    expect(certificateHref("a/b", "c d")).toBe(
      "/security/certificates/a%2Fb/c%20d",
    );
  });
});

// --------------------------------------------------------------------------
// eso-health
// --------------------------------------------------------------------------

describe("esoHealthView", () => {
  function es(over: Record<string, unknown> = {}) {
    return { namespace: "apps", name: "db-creds", status: "Synced", ...over };
  }

  test("an empty payload is readable and reports nothing to sync", () => {
    const view = esoHealthView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.rows).toEqual([]);
  });

  test("a payload this build cannot read is not an empty one", () => {
    const view = esoHealthView({ externalSecrets: [] }, 5);
    expect(view.readable).toBe(false);
    expect(view.total).toBe(0);
  });

  test("every drift state is distinct, including the two that are not answers", () => {
    // Four states, not three, and none of them is a synonym for another.
    //
    // `InSync` and `Drifted` are observations. `Unknown` is ESO saying it
    // could not determine drift for this ExternalSecret -- the provider does
    // not populate syncedResourceVersion, the target Secret was deleted, the
    // account cannot read it. `unobserved` is the poller not having reached
    // this one yet, which the wire contract states explicitly must be read as
    // "neither InSync nor Drifted" rather than folded into either.
    const view = esoHealthView(
      [
        es({ name: "a", lastObservedDriftStatus: "InSync" }),
        es({
          name: "b",
          lastObservedDriftStatus: "Drifted",
          status: "Drifted",
        }),
        es({ name: "c", lastObservedDriftStatus: "Unknown" }),
        es({ name: "d" }),
      ],
      10,
    );
    expect(view.drift).toEqual({
      Drifted: 1,
      Unknown: 1,
      unobserved: 1,
      InSync: 1,
    });
    expect(view.drift.InSync + view.drift.Unknown + view.drift.unobserved).toBe(
      3,
    );
  });

  test("an unhealthy row carries its own drift reading, undetermined included", () => {
    // Rows are the unhealthy ones, so the drift state has to travel with the
    // row rather than only into the cluster-wide counts -- otherwise the one
    // secret an operator would open the card for is the one whose drift
    // reading the card drops.
    const view = esoHealthView(
      [
        es({
          name: "cannot-tell",
          status: "Stale",
          lastObservedDriftStatus: "Unknown",
        }),
      ],
      5,
    );
    expect(view.rows[0].drift).toBe("Unknown");
  });

  test("an unreadable drift value is undetermined, not in sync", () => {
    const view = esoHealthView(
      [
        es({ name: "a", lastObservedDriftStatus: "" }),
        es({ name: "b", lastObservedDriftStatus: "Whatever" }),
        es({ name: "c", lastObservedDriftStatus: 7 }),
      ],
      10,
    );
    expect(view.drift.InSync).toBe(0);
    expect(view.drift.unobserved).toBe(3);
  });

  test("the declared drift states are the four the view counts", () => {
    expect([...ESO_DRIFT_STATES]).toEqual([
      "Drifted",
      "Unknown",
      "unobserved",
      "InSync",
    ]);
  });

  test("sync state counts split by the status the server derived", () => {
    const view = esoHealthView(
      [
        es({ name: "a", status: "Synced" }),
        es({ name: "b", status: "SyncFailed" }),
        es({ name: "c", status: "Stale" }),
        es({ name: "d", status: "Refreshing" }),
        es({ name: "e", status: "Drifted" }),
        es({ name: "f", status: "Unknown" }),
      ],
      10,
    );
    expect(view.total).toBe(6);
    expect(view.synced).toBe(1);
    expect(view.failing).toBe(1);
    expect(view.stale).toBe(1);
    // Everything that is not Synced: SyncFailed, Stale, Refreshing, Drifted
    // and Unknown. Refreshing is in there deliberately -- a secret mid-sync is
    // not yet a secret that synced.
    expect(view.unhealthy).toBe(5);
  });

  test("rows rank a failed sync above a stale one above a drifted one", () => {
    const view = esoHealthView(
      [
        es({ name: "healthy", status: "Synced" }),
        es({ name: "drifted", status: "Drifted" }),
        es({ name: "failed", status: "SyncFailed" }),
        es({ name: "refreshing", status: "Refreshing" }),
        es({ name: "stale", status: "Stale" }),
      ],
      10,
    );
    expect(view.rows.map((r) => r.name)).toEqual([
      "failed",
      "stale",
      "drifted",
      "refreshing",
    ]);
  });

  test("a healthy fleet produces no rows at all", () => {
    const view = esoHealthView([es({ name: "a" }), es({ name: "b" })], 5);
    expect(view.rows).toEqual([]);
    expect(view.synced).toBe(2);
    expect(view.unhealthy).toBe(0);
  });

  test("an entry naming no ExternalSecret is counted rather than rendered", () => {
    const view = esoHealthView([es(), {}, "x"], 5);
    expect(view.unreadableRows).toBe(2);
    expect(view.total).toBe(1);
  });

  test("the full page and per-secret links are encoded", () => {
    expect(EXTERNAL_SECRETS_PAGE_HREF).toBe(
      "/external-secrets/external-secrets",
    );
    expect(externalSecretHref("apps", "db creds")).toBe(
      "/external-secrets/external-secrets/apps/db%20creds",
    );
  });
});

// --------------------------------------------------------------------------
// velero-backups
// --------------------------------------------------------------------------

describe("backupsView", () => {
  function backup(over: Record<string, unknown> = {}) {
    return {
      name: "daily-20260920",
      namespace: "velero",
      phase: "Completed",
      startTime: "2026-09-20T01:00:00Z",
      errors: 0,
      warnings: 0,
      ...over,
    };
  }

  test("an empty payload is readable and says no backups exist", () => {
    const view = backupsView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.worst).toBeNull();
    expect(view.rows).toEqual([]);
  });

  test("a payload this build cannot read is not an empty one", () => {
    expect(backupsView(null, 5).readable).toBe(false);
    expect(backupsView({ backups: [] }, 5).readable).toBe(false);
  });

  test("a failed backup outranks an in-progress one in the roll-up", () => {
    // The headline the card leads with. A cluster whose last backup failed
    // while another is running is a cluster with a failed backup, and a
    // roll-up that led with "in progress" would read as reassurance.
    const view = backupsView(
      [
        backup({ name: "running", phase: "InProgress" }),
        backup({ name: "broken", phase: "Failed" }),
        backup({ name: "ok", phase: "Completed" }),
      ],
      10,
    );
    expect(view.worst).toBe("failed");
    expect(view.rows[0].name).toBe("broken");
    expect(view.counts.failed).toBe(1);
    expect(view.counts.inProgress).toBe(1);
    expect(view.counts.completed).toBe(1);
  });

  test("in progress outranks completed when nothing has failed", () => {
    const view = backupsView(
      [
        backup({ name: "ok", phase: "Completed" }),
        backup({ name: "running", phase: "InProgress" }),
      ],
      10,
    );
    expect(view.worst).toBe("inProgress");
    expect(view.rows[0].name).toBe("running");
  });

  test("every backup completed: the card has a clear state to report", () => {
    const view = backupsView(
      [backup({ name: "a" }), backup({ name: "b" })],
      10,
    );
    expect(view.worst).toBe("completed");
    expect(view.counts.completed).toBe(2);
    expect(view.rows).toEqual([]);
    expect(view.lastCompleted).toBe("2026-09-20T01:00:00Z");
  });

  test("a partial failure is a failure, matching the backups page", () => {
    // `getPhaseCategory` classifies anything containing "failed" as an error,
    // PartiallyFailed included. The card reuses that function rather than
    // keeping a second opinion, so the dashboard and the backups table never
    // disagree about the colour of the same row.
    const view = backupsView([backup({ phase: "PartiallyFailed" })], 10);
    expect(view.counts.failed).toBe(1);
    expect(view.worst).toBe("failed");
  });

  test("a phase nobody recognises is neither a success nor a failure", () => {
    const view = backupsView([backup({ name: "odd", phase: "Zorb" })], 10);
    expect(view.counts.other).toBe(1);
    expect(view.counts.completed).toBe(0);
    expect(view.counts.failed).toBe(0);
    expect(view.worst).toBe("other");
  });

  test("rows rank worst first and then newest first inside a rank", () => {
    const view = backupsView(
      [
        backup({
          name: "old-fail",
          phase: "Failed",
          startTime: "2026-09-01T00:00:00Z",
        }),
        backup({
          name: "new-fail",
          phase: "Failed",
          startTime: "2026-09-19T00:00:00Z",
        }),
        backup({ name: "running", phase: "InProgress", startTime: "" }),
      ],
      10,
    );
    expect(view.rows.map((r) => r.name)).toEqual([
      "new-fail",
      "old-fail",
      "running",
    ]);
  });

  test("the row list honours the caller's cap while the counts do not", () => {
    const backups = Array.from({ length: 7 }, (_, i) =>
      backup({ name: `b${i}`, phase: "Failed" }),
    );
    const view = backupsView(backups, 3);
    expect(view.rows).toHaveLength(3);
    expect(view.counts.failed).toBe(7);
  });

  test("an entry naming no backup is counted rather than rendered", () => {
    const view = backupsView([backup(), { phase: "Failed" }, null], 5);
    expect(view.unreadableRows).toBe(2);
    expect(view.total).toBe(1);
  });

  test("the full page link is the backups list", () => {
    expect(BACKUPS_PAGE_HREF).toBe("/backup/backups");
  });
});

// --------------------------------------------------------------------------
// snapshot-health
// --------------------------------------------------------------------------

describe("snapshotHealthView", () => {
  function snap(over: Record<string, unknown> = {}) {
    return {
      namespace: "apps",
      name: "pg-2026-09-20",
      readyToUse: true,
      sourcePVC: "pg-data",
      ...over,
    };
  }

  test("an empty payload is readable and says no snapshots exist", () => {
    const view = snapshotHealthView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.worst).toBeNull();
    expect(view.rows).toEqual([]);
  });

  test("a payload this build cannot read is not an empty one", () => {
    expect(snapshotHealthView(undefined, 5).readable).toBe(false);
    expect(snapshotHealthView("snapshots", 5).readable).toBe(false);
  });

  test("an error outranks a pending snapshot, which outranks a ready one", () => {
    const view = snapshotHealthView(
      [
        snap({ name: "ready" }),
        snap({ name: "pending", readyToUse: false }),
        snap({
          name: "broken",
          readyToUse: false,
          errorMessage: "class not found",
        }),
      ],
      10,
    );
    expect(view.worst).toBe("error");
    expect(view.counts).toEqual({ error: 1, pending: 1, ready: 1 });
    expect(view.rows.map((r) => r.name)).toEqual(["broken", "pending"]);
  });

  test("a snapshot carrying an error is an error even if it reports ready", () => {
    // `readyToUse` and `status.error.message` are independent fields on the
    // CRD and a snapshot can carry both -- a restore that succeeded after a
    // retry, or a controller that set one and not the other. An error nobody
    // surfaced is the worse failure, so it wins.
    const view = snapshotHealthView(
      [snap({ readyToUse: true, errorMessage: "quota exceeded" })],
      5,
    );
    expect(view.counts.error).toBe(1);
    expect(view.counts.ready).toBe(0);
  });

  test("every snapshot ready: the card has a clear state to report", () => {
    const view = snapshotHealthView(
      [snap({ name: "a" }), snap({ name: "b" })],
      5,
    );
    expect(view.worst).toBe("ready");
    expect(view.counts.ready).toBe(2);
    expect(view.rows).toEqual([]);
  });

  test("the row list honours the caller's cap while the counts do not", () => {
    const snaps = Array.from({ length: 6 }, (_, i) =>
      snap({ name: `s${i}`, readyToUse: false }),
    );
    const view = snapshotHealthView(snaps, 2);
    expect(view.rows).toHaveLength(2);
    expect(view.counts.pending).toBe(6);
  });

  test("an entry naming no snapshot is counted rather than rendered", () => {
    const view = snapshotHealthView([snap(), { namespace: "apps" }, 3], 5);
    expect(view.unreadableRows).toBe(2);
    expect(view.total).toBe(1);
  });

  test("the full page link is the snapshots list", () => {
    expect(SNAPSHOTS_PAGE_HREF).toBe("/storage/snapshots");
  });
});
