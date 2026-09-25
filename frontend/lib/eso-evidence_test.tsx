/** @jsxImportSource preact */
import { expect, test } from "bun:test";
import { render } from "preact-render-to-string";
import ESOEvidencePanel from "@/src/islands/ESOEvidencePanel.tsx";
import { ApiError } from "./api.ts";
import {
  classifyEvidenceError,
  describeProjection,
  EVIDENCE_SUPPORT,
  evidenceNamespaceSegment,
  evidenceTabsFor,
  generatedExternalSecrets,
  isStaleResponse,
  mergeHistoryPages,
  unavailableMessage,
} from "./eso-evidence.ts";
import type {
  EvidenceKind,
  EvidenceUnavailableReason,
  HistoryEntry,
} from "./eso-types.ts";

const ALL_KINDS: EvidenceKind[] = [
  "externalsecrets",
  "clusterexternalsecrets",
  "secretstores",
  "clustersecretstores",
  "pushsecrets",
];

function apiError(status: number, reason?: string, message?: string) {
  return new ApiError(status, status, message, {
    error: { code: status, message, reason },
  });
}

function entry(id: number, attemptAt: string): HistoryEntry {
  return {
    id,
    attemptAt,
    outcome: "success",
    reason: "SecretSynced",
    diffKeyCounts: { added: 0, removed: 0, changed: 0 },
  };
}

// --- D5 support matrix (R12) -------------------------------------------------

test("only ExternalSecret has real history (R12)", () => {
  for (const kind of ALL_KINDS) {
    const want = kind === "externalsecrets" ? "real" : undefined;
    if (want) {
      expect(EVIDENCE_SUPPORT[kind].history).toBe("real");
    } else {
      expect(EVIDENCE_SUPPORT[kind].history).not.toBe("real");
    }
  }
  expect(EVIDENCE_SUPPORT.clusterexternalsecrets.history).toBe("children");
});

test("every kind supports YAML and Events", () => {
  for (const kind of ALL_KINDS) {
    expect(EVIDENCE_SUPPORT[kind].yaml).toBe(true);
    expect(EVIDENCE_SUPPORT[kind].events).toBe(true);
  }
});

test("evidenceTabsFor derives the tab strip from the matrix", () => {
  expect(evidenceTabsFor("externalsecrets").map((t) => t.label)).toEqual([
    "YAML",
    "Events",
    "History",
  ]);
  for (const kind of [
    "secretstores",
    "clustersecretstores",
    "pushsecrets",
  ] as const) {
    const labels = evidenceTabsFor(kind).map((t) => t.label);
    expect(labels).toEqual(["YAML", "Events"]);
    expect(labels).not.toContain("History");
  }
  // A ClusterExternalSecret's attempts belong to its children, so the tab is
  // named for them rather than claiming a history of its own.
  const ces = evidenceTabsFor("clusterexternalsecrets");
  expect(ces.map((t) => t.label)).toEqual([
    "YAML",
    "Events",
    "Generated ExternalSecrets",
  ]);
  expect(ces.map((t) => t.label)).not.toContain("History");
});

// --- Error classification (R3) ------------------------------------------------

test("classifyEvidenceError maps every server answer to a distinct reason", () => {
  const cases: Array<[unknown, EvidenceUnavailableReason]> = [
    [apiError(403), "forbidden"],
    [apiError(403, "events_forbidden"), "forbidden"],
    [apiError(501, "remote_history_unsupported"), "remote_unsupported"],
    [apiError(501, "remote_events_unsupported"), "remote_unsupported"],
    [apiError(503, "eso_not_detected"), "eso_not_detected"],
    [apiError(503, "history_unavailable"), "history_unavailable"],
    [apiError(503, "discovery_unavailable"), "discovery_unavailable"],
    [apiError(503, "some_future_reason"), "error"],
    [apiError(503), "error"],
    [apiError(404, "evidence_kind_not_served"), "unsupported_kind"],
    [apiError(404), "not_found"],
    [apiError(500), "error"],
    [apiError(400, "invalid_cursor"), "error"],
    [new Error("network down"), "error"],
    ["not an error", "error"],
  ];
  for (const [err, want] of cases) {
    expect(classifyEvidenceError(err)).toBe(want);
  }
});

test("every unavailable reason has its own message", () => {
  const reasons: EvidenceUnavailableReason[] = [
    "remote_unsupported",
    "eso_not_detected",
    "history_unavailable",
    "discovery_unavailable",
    "forbidden",
    "not_found",
    "unsupported_kind",
    "error",
  ];
  const messages = reasons.map((r) => unavailableMessage(r));
  expect(new Set(messages).size).toBe(reasons.length);
  for (const m of messages) {
    expect(m.length).toBeGreaterThan(0);
    // None of them may read as "there is simply nothing here".
    expect(m.toLowerCase()).not.toContain("no data");
  }
});

// --- History paging ----------------------------------------------------------

test("mergeHistoryPages appends, drops duplicate ids, keeps newest first", () => {
  const prev = [
    entry(9, "2026-09-10T12:00:03Z"),
    entry(8, "2026-09-10T12:00:02Z"),
  ];
  const next = [
    entry(8, "2026-09-10T12:00:02Z"), // duplicate across the page boundary
    entry(7, "2026-09-10T12:00:01Z"),
    entry(6, "2026-09-10T12:00:01Z"), // same instant, lower id
  ];
  const merged = mergeHistoryPages(prev, next);
  expect(merged.map((e) => e.id)).toEqual([9, 8, 7, 6]);
});

test("mergeHistoryPages restores newest-first order if a page arrives out of order", () => {
  const merged = mergeHistoryPages(
    [entry(1, "2026-09-10T12:00:00Z")],
    [entry(2, "2026-09-10T12:00:05Z")],
  );
  expect(merged.map((e) => e.id)).toEqual([2, 1]);
});

test("mergeHistoryPages does not mutate its inputs", () => {
  const prev = [entry(2, "2026-09-10T12:00:02Z")];
  const next = [entry(1, "2026-09-10T12:00:01Z")];
  mergeHistoryPages(prev, next);
  expect(prev.map((e) => e.id)).toEqual([2]);
  expect(next.map((e) => e.id)).toEqual([1]);
});

// --- Late-response guard (R1) ---------------------------------------------------

test("isStaleResponse discards a response for a different object", () => {
  expect(isStaleResponse("uid-new", "uid-old")).toBe(true);
  expect(isStaleResponse("uid-1", "uid-1")).toBe(false);
  // A response that cannot prove its identity is never trusted.
  expect(isStaleResponse("uid-1", "")).toBe(true);
  expect(isStaleResponse("uid-1", undefined)).toBe(true);
});

// --- Projection copy (AE4) -------------------------------------------------------

test("describeProjection explains what is hidden only at outcome-only", () => {
  expect(describeProjection({ level: "full", droppedFields: [] })).toBeNull();
  const copy = describeProjection({
    level: "outcome-only",
    droppedFields: ["message", "messageTruncated", "diffKeysAdded"],
  });
  expect(copy).not.toBeNull();
  expect(copy).toContain("requires Secret read");
});

// --- Addressing -------------------------------------------------------------------

test("evidenceNamespaceSegment renders cluster scope as _", () => {
  expect(evidenceNamespaceSegment(null)).toBe("_");
  expect(evidenceNamespaceSegment("apps")).toBe("apps");
  expect(evidenceNamespaceSegment("a b")).toBe("a%20b");
});

// --- ClusterExternalSecret children -----------------------------------------------

test("generatedExternalSecrets links each child ExternalSecret by namespace", () => {
  const children = generatedExternalSecrets({
    name: "shared-db",
    externalSecretBaseName: "db-creds",
    provisionedNamespaces: ["apps", "billing"],
    failedNamespaces: ["legacy"],
  });
  expect(children).toEqual([
    {
      namespace: "apps",
      name: "db-creds",
      provisioned: true,
      href: "/external-secrets/external-secrets/apps/db-creds",
    },
    {
      namespace: "billing",
      name: "db-creds",
      provisioned: true,
      href: "/external-secrets/external-secrets/billing/db-creds",
    },
    {
      namespace: "legacy",
      name: "db-creds",
      provisioned: false,
      href: "/external-secrets/external-secrets/legacy/db-creds",
    },
  ]);
});

test("generatedExternalSecrets defaults the child name to the CES name", () => {
  const [child] = generatedExternalSecrets({
    name: "shared-db",
    provisionedNamespaces: ["apps"],
  });
  expect(child.name).toBe("shared-db");
  expect(generatedExternalSecrets({ name: "x" })).toEqual([]);
});

// --- The rendered panel ---------------------------------------------------------

/** Tab labels in the panel's server-rendered markup (no effects run). */
function renderedTabs(kind: EvidenceKind): string[] {
  const html = render(
    <ESOEvidencePanel
      kind={kind}
      namespace={kind.startsWith("cluster") ? null : "apps"}
      name="obj"
      uid="uid-1"
    />,
  );
  return [...html.matchAll(/role="tab"[^>]*>([^<]+)</g)].map((m) => m[1]);
}

test("the rendered panel shows exactly the matrix's tabs for every kind", () => {
  for (const kind of ALL_KINDS) {
    expect(renderedTabs(kind)).toEqual(
      evidenceTabsFor(kind).map((t) => t.label),
    );
  }
  // The pairs D5 rules out never appear, whatever the matrix helper says.
  for (const kind of [
    "secretstores",
    "clustersecretstores",
    "pushsecrets",
    "clusterexternalsecrets",
  ] as const) {
    expect(renderedTabs(kind)).not.toContain("History");
  }
});

test("the rendered panel ignores an initialTab its kind does not support", () => {
  const html = render(
    <ESOEvidencePanel
      kind="secretstores"
      namespace="apps"
      name="vault"
      uid="uid-1"
      initialTab="history"
    />,
  );
  expect(html).not.toContain(">History<");
  expect(html).toMatch(/aria-selected="true"[^>]*>YAML</);
});
