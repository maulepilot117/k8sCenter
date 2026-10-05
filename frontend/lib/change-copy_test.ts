import { expect, test } from "bun:test";
import { ApiError } from "./api.ts";
import {
  checkReasonText,
  executionBadge,
  executionExplanation,
  isFinalVerification,
  isOperationId,
  ownershipCopy,
  ownershipFailureText,
  ownershipRefsFromPreview,
  receiptHref,
  repairEligibility,
  repairHref,
  trackingAvailabilityFromError,
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

test("ownership refs take the group from the YAML only when the document agrees", () => {
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
  const refs = ownershipRefsFromPreview(
    [
      {
        index: 0,
        kind: "Deployment",
        name: "api",
        namespace: "web",
        valid: true,
      },
      {
        index: 1,
        kind: "ConfigMap",
        name: "cfg",
        namespace: "web",
        valid: true,
      },
      { index: 2, kind: "Job", name: "", valid: true },
    ],
    yaml,
  );
  expect(refs).toEqual([
    {
      kind: "Deployment",
      name: "api",
      namespace: "web",
      group: "apps",
      version: "v1",
    },
    { kind: "ConfigMap", name: "cfg", namespace: "web", version: "v1" },
  ]);
});

test("ownership refs drop the guessed group when the YAML is out of step", () => {
  const refs = ownershipRefsFromPreview(
    [{ index: 0, kind: "Deployment", name: "api", valid: true }],
    "apiVersion: v1\nkind: ConfigMap\nmetadata: { name: other }\n",
  );
  expect(refs).toEqual([{ kind: "Deployment", name: "api" }]);
  expect(
    ownershipRefsFromPreview(
      [{ index: 0, kind: "Deployment", name: "api", valid: true }],
      "::: not yaml [",
    ),
  ).toEqual([{ kind: "Deployment", name: "api" }]);
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

test("repair and receipt hrefs carry only the id", () => {
  expect(repairHref(ID)).toBe(`/tools/yaml-apply?repairOf=${ID}`);
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
