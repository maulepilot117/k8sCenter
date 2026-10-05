import { expect, test } from "bun:test";
import { ApiError } from "./api.ts";
import {
  assembleOwnership,
  checkReasonText,
  clusterDisplayName,
  executionBadge,
  executionExplanation,
  hiddenText,
  isFinalVerification,
  isOperationId,
  managedSummary,
  OWNERSHIP_REASON_UNREADABLE,
  ownershipCopy,
  ownershipFailureText,
  ownershipPlanFromPreview,
  RECORDS_UNAVAILABLE,
  receiptHref,
  repairEligibility,
  repairHref,
  sameCluster,
  trackedRefusalText,
  trackingAvailabilityFromError,
  unreadableOwnership,
  verificationBadge,
  verificationPollDelayMs,
  verificationReasons,
} from "./change-copy.ts";
import type {
  CheckView,
  OwnedByApp,
  OwnershipConfidence,
  OwnershipView,
  ReceiptState,
  VerificationState,
} from "./change-types.ts";

const ID = "6f1d3c52-4b1e-4f0a-9c53-0d7a2b8e1f64";

function ownership(
  confidence: OwnershipConfidence,
  reason: string,
  over: Partial<OwnershipView> = {},
): OwnershipView {
  return {
    object: { clusterId: "local", kind: "Deployment", name: "api" },
    controller: "none",
    confidence,
    reason,
    identityBasis: "group-kind-namespace-name",
    uidConfirmed: false,
    writableGitSource: false,
    observedAt: "2026-10-01T00:00:00Z",
    ...over,
  };
}

function app(over: Partial<OwnedByApp> = {}): OwnedByApp {
  return {
    appId: "argo:argocd:shop",
    tool: "argocd",
    kind: "Application",
    namespace: "argocd",
    name: "shop",
    source: { repoURL: "https://git.example.com/shop.git", path: "deploy" },
    suspended: false,
    ...over,
  };
}

// --- execution / verification ---------------------------------------------

test("execution and verification badges never share a label", () => {
  const exec: ReceiptState[] = [
    "previewed",
    "applying",
    "applied",
    "partial",
    "failed",
    "unknown",
  ];
  const ver: VerificationState[] = [
    "pending",
    "verifying",
    "verified",
    "inconclusive",
    "verification_failed",
  ];
  const execLabels = exec.map((s) => executionBadge(s).label);
  const verLabels = ver.map((s) => verificationBadge(s).label);
  for (const l of execLabels) expect(verLabels).not.toContain(l);
  expect(executionBadge("applied").label).toBe("Applied");
  expect(verificationBadge("pending").label).toBe("Verification pending");
});

test("inconclusive verification and an unknown outcome are never toned as success", () => {
  expect(verificationBadge("inconclusive").tone).toBe("neutral");
  expect(executionBadge("unknown").tone).not.toBe("ok");
  expect(executionBadge("unknown").label).toBe("Outcome unknown");
  // Only the states that really mean success are green.
  expect(executionBadge("applied").tone).toBe("ok");
  expect(verificationBadge("verified").tone).toBe("ok");
  expect(verificationBadge("verification_failed").tone).toBe("crit");
});

test("unknown states from a newer server render neutrally, verbatim", () => {
  expect(executionBadge("rolled_sideways")).toEqual({
    label: "rolled_sideways",
    tone: "neutral",
  });
  expect(verificationBadge("pondering").tone).toBe("neutral");
});

test("an unknown outcome explains itself and offers no retry", () => {
  const text = executionExplanation("unknown") ?? "";
  expect(text).toContain("Check the live objects");
  expect(text).toContain("no one-click retry");
  expect(executionExplanation("applied")).toBeNull();
  expect(executionExplanation("partial")).toContain("Nothing was rolled back");
});

test("final verification states stop polling; the others do not", () => {
  expect(isFinalVerification("verified")).toBe(true);
  expect(isFinalVerification("inconclusive")).toBe(true);
  expect(isFinalVerification("verification_failed")).toBe(true);
  expect(isFinalVerification("pending")).toBe(false);
  expect(isFinalVerification("verifying")).toBe(false);
});

test("poll delay honours retryAfterSeconds and defaults to 5s", () => {
  expect(verificationPollDelayMs(3)).toBe(3000);
  expect(verificationPollDelayMs(undefined)).toBe(5000);
  expect(verificationPollDelayMs(0)).toBe(5000);
  expect(verificationPollDelayMs(-1)).toBe(5000);
  expect(verificationPollDelayMs(3, 1)).toBe(3);
  expect(verificationPollDelayMs(undefined, 2)).toBe(10);
});

test("check reasons are spelled out; unknown codes are shown verbatim", () => {
  expect(checkReasonText("kind_not_supported")).toBe(
    "This kind has no supported readiness postcondition, so k8sCenter cannot verify it.",
  );
  expect(checkReasonText("window_expired")).toContain("verification window");
  expect(checkReasonText("brand_new_reason")).toBe("Reason: brand_new_reason");
});

test("verificationReasons lists each non-passing reason once, redacted ones included", () => {
  const checks: CheckView[] = [
    {
      checkId: "a",
      status: "inconclusive",
      severity: "info",
      reason: "kind_not_supported",
      observedAt: "t",
    },
    {
      checkId: "b",
      status: "inconclusive",
      severity: "info",
      reason: "kind_not_supported",
      observedAt: "t",
    },
    {
      checkId: "c",
      status: "pass",
      severity: "info",
      reason: "ok",
      observedAt: "t",
    },
    {
      checkId: "d",
      status: "inconclusive",
      severity: "info",
      reason: "read_forbidden",
      redacted: true,
      observedAt: "t",
    },
  ];
  expect(verificationReasons(checks)).toEqual([
    checkReasonText("kind_not_supported"),
    checkReasonText("read_forbidden"),
  ]);
});

// --- ownership ---------------------------------------------------------------

test("confirmed ownership names the application and warns about revert", () => {
  const c = ownershipCopy(
    ownership("confirmed", "confirmed-argo-status", {
      controller: "argocd",
      apps: [app()],
    }),
  );
  expect(c.tone).toBe("warn");
  expect(c.text).toBe(
    "Managed by Argo CD application argo:argocd:shop. Applying here changes the live object; the controller may revert it on its next sync.",
  );
});

test("ownership copy never offers a Git write, even with a repoURL present", () => {
  const all: OwnershipView[] = [
    ownership("confirmed", "confirmed-argo-status", {
      controller: "argocd",
      apps: [app()],
    }),
    ownership("conflicting", "both-claim", {
      controller: "both",
      apps: [app(), app({ appId: "flux-ks:flux-system:shop", tool: "fluxcd" })],
    }),
  ];
  for (const r of all) {
    const text = ownershipCopy(r).text.toLowerCase();
    for (const banned of ["pull request", "open a pr", "commit", "git"]) {
      expect(text).not.toContain(banned);
    }
    expect(text).not.toContain("https://");
  }
});

test("conflicting ownership names both controllers", () => {
  const c = ownershipCopy(
    ownership("conflicting", "both-claim", {
      controller: "both",
      apps: [app(), app({ appId: "flux-ks:flux-system:shop", tool: "fluxcd" })],
    }),
  );
  expect(c.label).toBe("Conflicting");
  expect(c.text).toContain("Two controllers claim this object");
  expect(c.text).toContain("Argo CD application argo:argocd:shop");
  expect(c.text).toContain("Flux CD application flux-ks:flux-system:shop");
});

test("hints-only is unknown, distinct from conflicting and from no-evidence", () => {
  const hints = ownershipCopy(ownership("unknown", "hints-only"));
  const none = ownershipCopy(ownership("unknown", "no-evidence"));
  const conflict = ownershipCopy(ownership("conflicting", "both-claim"));
  expect(hints.text).toBe(
    "This object carries GitOps labels or annotations, but no controller's inventory confirms it. Ownership is unknown.",
  );
  expect(new Set([hints.text, none.text, conflict.text]).size).toBe(3);
  expect(hints.tone).toBe("neutral");
});

test("every unknown reason has its own honest line", () => {
  const reasons = [
    "partial-visibility",
    "search-bound-exhausted",
    "argo-destination-unverified",
    "flux-remote-kubeconfig",
    "flux-helmrelease-no-inventory",
  ];
  const texts = reasons.map((r) => ownershipCopy(ownership("unknown", r)).text);
  expect(new Set(texts).size).toBe(reasons.length);
  for (const t of texts) expect(t).toContain("Ownership is unknown.");
});

test("forbidden and unavailable say the check could not run, never 'not managed'", () => {
  const cases = [
    ownership("forbidden", "argo-list-forbidden"),
    ownership("forbidden", "flux-list-forbidden"),
    ownership("unavailable", "argo-unavailable"),
    ownership("unavailable", "flux-unavailable"),
  ];
  for (const r of cases) {
    const c = ownershipCopy(r);
    expect(c.label).not.toBe("Not managed");
    expect(c.text.toLowerCase()).not.toContain("not managed");
  }
  expect(ownershipCopy(cases[0]).text).toBe(
    "You cannot list Argo CD applications there, so ownership could not be determined.",
  );
  expect(ownershipCopy(cases[2]).text).toContain("Argo CD did not respond");
});

test("no controller installed is its own line", () => {
  expect(
    ownershipCopy(ownership("unavailable", "no-controller-installed")).text,
  ).toBe(
    "No GitOps controller (Argo CD or Flux CD) is installed on this cluster.",
  );
});

test("apps-redacted hides the application and says why", () => {
  const c = ownershipCopy(ownership("forbidden", "apps-redacted"));
  expect(c.text).toContain("you no longer have access to it");
  expect(c.text).not.toContain("argo:");
});

test("partially redacted apps are counted, suspended apps noted", () => {
  const c = ownershipCopy(
    ownership("confirmed", "confirmed-argo-status", {
      controller: "argocd",
      apps: [app({ suspended: true })],
      redactedApps: 2,
    }),
  );
  expect(c.text).toContain("argo:argocd:shop is suspended");
  expect(c.text).toContain("2 more claiming applications are hidden");
});

test("a failed ownership request is never worded as unmanaged", () => {
  for (const status of [404, 503, 403, 504, 500]) {
    const text = ownershipFailureText(new ApiError(status, status, "x"));
    expect(text.toLowerCase()).not.toContain("not managed");
  }
  expect(ownershipFailureText(new Error("network"))).toContain("unknown");
});

// --- ownership plan ----------------------------------------------------------

type Doc = Parameters<typeof ownershipPlanFromPreview>[0][number];
const doc = (
  index: number,
  kind: string,
  name: string,
  namespace?: string,
): Doc => ({
  index,
  kind,
  name,
  valid: true,
  ...(namespace ? { namespace } : {}),
});

test("the plan takes each group from the YAML and skips generateName documents", () => {
  const yaml = [
    "apiVersion: apps/v1",
    "kind: Deployment",
    "metadata: { name: api, namespace: web }",
    "---",
    "---",
    "apiVersion: v1",
    "kind: ConfigMap",
    "metadata: { name: cfg, namespace: web }",
    "---",
    "apiVersion: batch/v1",
    "kind: Job",
    "metadata: { generateName: run- }",
  ].join("\n");
  const plan = ownershipPlanFromPreview(
    [
      doc(0, "Deployment", "api", "web"),
      doc(1, "ConfigMap", "cfg", "web"),
      doc(2, "Job", ""),
    ],
    yaml,
  );
  expect(plan.refs).toEqual([
    {
      kind: "Deployment",
      name: "api",
      namespace: "web",
      group: "apps",
      version: "v1",
    },
    { kind: "ConfigMap", name: "cfg", namespace: "web", version: "v1" },
  ]);
  expect(plan.slots).toEqual([{ ref: 0 }, { ref: 1 }]);
});

test("a namespace-less manifest is sent without a namespace for the server to default", () => {
  // The applier puts a namespaced object without metadata.namespace into
  // "default"; only the server knows the kind's scope, so the client sends
  // what it knows and never fakes one (a cluster-scoped kind has none).
  const plan = ownershipPlanFromPreview(
    [doc(0, "Deployment", "api"), doc(1, "Namespace", "team-a")],
    "apiVersion: apps/v1\nkind: Deployment\nmetadata: { name: api }\n---\napiVersion: v1\nkind: Namespace\nmetadata: { name: team-a }\n",
  );
  expect(plan.refs).toEqual([
    { kind: "Deployment", name: "api", group: "apps", version: "v1" },
    { kind: "Namespace", name: "team-a", version: "v1" },
  ]);
});

test("a duplicate key is read last-wins, as the server does", () => {
  const plan = ownershipPlanFromPreview(
    [doc(0, "Deployment", "api", "web")],
    "apiVersion: apps/v1\nkind: Deployment\nkind: Deployment\nmetadata: { name: api, namespace: web }\n",
  );
  expect(plan.refs[0]?.group).toBe("apps");
  expect(plan.slots).toEqual([{ ref: 0 }]);
});

test("one unparseable document loses its group; the others keep theirs", () => {
  const yaml = [
    "apiVersion: apps/v1",
    "kind: Deployment",
    "metadata: { name: api, namespace: web }",
    "---",
    "apiVersion: apps/v1",
    "kind: StatefulSet",
    "metadata: { name: db, namespace: web",
    "---",
    "apiVersion: apps/v1",
    "kind: DaemonSet",
    "metadata: { name: agent, namespace: web }",
  ].join("\n");
  const plan = ownershipPlanFromPreview(
    [
      doc(0, "Deployment", "api", "web"),
      doc(1, "StatefulSet", "db", "web"),
      doc(2, "DaemonSet", "agent", "web"),
    ],
    yaml,
  );
  expect(plan.refs.map((r) => [r.kind, r.group])).toEqual([
    ["Deployment", "apps"],
    ["DaemonSet", "apps"],
  ]);
  expect(plan.slots).toEqual([
    { ref: 0 },
    {
      unreadable: {
        kind: "StatefulSet",
        name: "db",
        namespace: "web",
      },
    },
    { ref: 1 },
  ]);
});

test("a JSON stream the browser cannot split is not checked rather than sent group-less", () => {
  const plan = ownershipPlanFromPreview(
    [doc(0, "Deployment", "a", "web"), doc(1, "Deployment", "b", "web")],
    '{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"a","namespace":"web"}}{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"b","namespace":"web"}}',
  );
  // Nothing is sent without its group: a group-less Deployment would be
  // resolved as a core-group object and could read as unmanaged.
  for (const ref of plan.refs) expect(ref.group).toBe("apps");
  expect(plan.slots.filter((s) => "unreadable" in s)).toHaveLength(
    2 - plan.refs.length,
  );
});

test("a document out of step with the server's is not checked", () => {
  const plan = ownershipPlanFromPreview(
    [doc(0, "Deployment", "api")],
    "apiVersion: v1\nkind: ConfigMap\nmetadata: { name: other }\n",
  );
  expect(plan.refs).toEqual([]);
  expect(plan.slots).toEqual([
    { unreadable: { kind: "Deployment", name: "api" } },
  ]);
});

test("an unreadable row is unknown and never 'Not managed'", () => {
  const row = unreadableOwnership({ kind: "Deployment", name: "api" }, "local");
  expect(row.confidence).toBe("unknown");
  expect(row.writableGitSource).toBe(false);
  const copy = ownershipCopy(row);
  expect(copy.label).toBe("Not checked");
  expect(copy.label).not.toBe("Not managed");
  expect(copy.text).toContain("Ownership is unknown.");
});

test("assembleOwnership keeps document order and counts refs past the cap", () => {
  const plan = {
    refs: [
      { kind: "A", name: "a" },
      { kind: "B", name: "b" },
      { kind: "C", name: "c" },
    ],
    slots: [
      { ref: 0 },
      { unreadable: { kind: "X", name: "x" } },
      { ref: 1 },
      { ref: 2 },
    ],
  };
  const answer = (kind: string) =>
    ownership("unknown", "no-evidence", {
      object: { clusterId: "local", kind, name: kind.toLowerCase() },
    });
  const out = assembleOwnership(plan, [answer("A"), answer("B")], 2, "local");
  expect(out.results.map((r) => r.object.kind)).toEqual(["A", "X", "B"]);
  expect(out.results[1].reason).toBe(OWNERSHIP_REASON_UNREADABLE);
  expect(out.omitted).toBe(1);
});

// --- shared sentences -----------------------------------------------------------

test("hiddenText reads correctly for one and many, and is empty for none", () => {
  expect(hiddenText("objects", 1)).toBe(
    "1 object is hidden because you no longer have access to it.",
  );
  expect(hiddenText("objects", 2)).toBe(
    "2 objects are hidden because you no longer have access to them.",
  );
  expect(hiddenText("checks", 3)).toContain("Details of 3 checks are hidden");
  expect(hiddenText("ownership", 1)).toBe(
    "Ownership of 1 object is hidden because you no longer have access to it.",
  );
  expect(hiddenText("apps", 2)).toContain("2 more claiming applications are");
  expect(hiddenText("objects", 0)).toBe("");
});

test("managedSummary is null for none and grammatical for one or many", () => {
  expect(managedSummary(0)).toBeNull();
  expect(managedSummary(1)).toContain("1 of these objects is managed");
  expect(managedSummary(1)).toContain("the controller may revert it");
  expect(managedSummary(3)).toContain("3 of these objects are managed");
  expect(managedSummary(3)).toContain("their controllers may revert them");
});

test("a store failure only says nothing was applied by this request when the server said so", () => {
  const base = {
    reason: "receipt_store_unavailable" as const,
    message: "hook text",
  };
  const notApplied = trackedRefusalText({ ...base, applied: false });
  expect(notApplied).toContain("Nothing was applied by this request.");
  // applied:false covers this request only; it never claims the operation.
  expect(notApplied).not.toContain("confirmed nothing");
  for (const applied of [true, undefined]) {
    const text = trackedRefusalText({ ...base, applied });
    expect(text).not.toContain("nothing was applied");
    expect(text).toContain("did not confirm whether anything was applied");
  }
  expect(
    trackedRefusalText({
      ...base,
      applied: false,
      retrySameOperationId: false,
    }),
  ).toContain("The next apply starts a new change.");
  const sameId = trackedRefusalText({
    ...base,
    applied: false,
    retrySameOperationId: true,
  });
  expect(sameId).toContain("Apply again to retry this same change");
  expect(sameId).toContain(
    "An earlier attempt with this operation id may already have applied",
  );
  expect(sameId).not.toContain("new change");
  expect(
    trackedRefusalText({ reason: "operation_id_reused", message: "verbatim" }),
  ).toBe("verbatim");
});

test("cluster helpers name the local cluster and compare ids", () => {
  expect(clusterDisplayName("local")).toBe("Local cluster");
  expect(clusterDisplayName("prod-eu")).toBe("prod-eu");
  expect(sameCluster("", "local")).toBe(true);
  expect(sameCluster("a", "b")).toBe(false);
  expect(RECORDS_UNAVAILABLE).toContain("Change records are unavailable");
});

// --- availability / repair ----------------------------------------------------

test("tracking availability distinguishes 404, 503 and anything else", () => {
  expect(
    trackingAvailabilityFromError(new ApiError(404, 404, "x")).status,
  ).toBe("unsupported");
  expect(
    trackingAvailabilityFromError(new ApiError(503, 503, "x")).status,
  ).toBe("unavailable");
  expect(
    trackingAvailabilityFromError(new ApiError(500, 500, "x")).status,
  ).toBe("unconfirmed");
  expect(trackingAvailabilityFromError(new TypeError("net")).status).toBe(
    "unconfirmed",
  );
});

test("repair is offered only for partial or failed receipts without Secrets", () => {
  expect(
    repairEligibility({ state: "partial", containsSecret: false }),
  ).toEqual({
    kind: "allowed",
  });
  expect(repairEligibility({ state: "failed", containsSecret: false })).toEqual(
    {
      kind: "allowed",
    },
  );
  expect(repairEligibility({ state: "failed", containsSecret: true })).toEqual({
    kind: "secret",
  });
  for (const state of [
    "applied",
    "unknown",
    "applying",
    "previewed",
  ] as const) {
    expect(repairEligibility({ state, containsSecret: false }).kind).toBe(
      "not-applicable",
    );
  }
});

test("repair and receipt hrefs carry only the id and the receipt's cluster", () => {
  expect(repairHref(ID)).toBe(`/tools/yaml-apply?repairOf=${ID}`);
  expect(repairHref(ID, "prod eu")).toBe(
    `/tools/yaml-apply?repairOf=${ID}&cluster=prod+eu`,
  );
  expect(receiptHref(ID)).toBe(`/changes/${ID}`);
  expect(receiptHref("a/b")).toBe("/changes/a%2Fb");
});

test("isOperationId accepts canonical UUIDv4 only", () => {
  expect(isOperationId(ID)).toBe(true);
  expect(isOperationId(ID.toUpperCase())).toBe(true);
  expect(isOperationId("6f1d3c52-4b1e-1f0a-9c53-0d7a2b8e1f64")).toBe(false);
  expect(isOperationId(`{${ID}}`)).toBe(false);
  expect(isOperationId("")).toBe(false);
  expect(isOperationId(null)).toBe(false);
});
