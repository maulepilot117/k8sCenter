/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import type {
  CheckView,
  ReceiptDetail,
  VerificationView,
} from "@/lib/change-types.ts";

/**
 * Render-level coverage for the change receipt page: what each execution and
 * verification combination reads as, what a redacted row shows, when Retry
 * is offered, and that verification polling stops at a final state and is
 * cancelled on unmount. fetch is routed by URL; everything else is real.
 */

const { default: ChangeReceipt } = await import("./ChangeReceipt.tsx");

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

const ID = "6f1d3c52-4b1e-4f0a-9c53-0d7a2b8e1f64";

interface Call {
  url: string;
  signal: AbortSignal | null | undefined;
}

type Reply = { status?: number; body: unknown };

let calls: Call[] = [];
let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
});

function receipt(over: Partial<ReceiptDetail> = {}): ReceiptDetail {
  return {
    operationId: ID,
    receiptUrl: `/v1/changes/${ID}`,
    ownerUsername: "alice",
    state: "applied",
    clusterId: "local",
    clusterGeneration: "local",
    targetGenerationChanged: false,
    contentDigest: "sha256:abc123",
    documentCount: 1,
    recordedThrough: 1,
    force: false,
    containsSecret: false,
    verification: { state: "pending", url: `/v1/changes/${ID}/verification` },
    createdAt: "2026-10-01T10:00:00Z",
    access: "owner",
    summary: {
      total: 1,
      created: 1,
      configured: 0,
      unchanged: 0,
      failed: 0,
      notRecorded: 0,
    },
    objects: [
      {
        index: 0,
        group: "apps",
        version: "v1",
        resource: "deployments",
        kind: "Deployment",
        namespace: "web",
        name: "api",
        action: "created",
      },
    ],
    redactedObjects: 0,
    checks: [],
    redactedChecks: 0,
    ownership: [],
    redactedOwnership: 0,
    ...over,
  };
}

function check(over: Partial<CheckView> = {}): CheckView {
  return {
    checkId: "postcondition",
    status: "inconclusive",
    severity: "info",
    reason: "kind_not_supported",
    message: "ConfigMap has no readiness postcondition",
    source: {
      clusterId: "local",
      resource: "configmaps",
      kind: "ConfigMap",
      namespace: "web",
      name: "cfg",
    },
    observedAt: "2026-10-01T10:00:01Z",
    ...over,
  };
}

/**
 * Answers the receipt and verification endpoints. Both take a queue: each
 * request takes the next reply, and the last one repeats.
 */
function stubFetch(get: Reply | Reply[], verification: Reply[] = []) {
  calls = [];
  originalFetch = globalThis.fetch;
  const gets = Array.isArray(get) ? get : [get];
  let g = 0;
  let v = 0;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, signal: init?.signal });
    let reply: Reply = {
      status: 599,
      body: { error: { code: 599, message: "unstubbed" } },
    };
    if (url === `/api/v1/changes/${ID}`) {
      reply = gets[Math.min(g, gets.length - 1)];
      g++;
    } else if (url === `/api/v1/changes/${ID}/verification`) {
      reply = verification[Math.min(v, verification.length - 1)] ?? reply;
      v++;
    }
    return Promise.resolve(
      new Response(JSON.stringify(reply.body), {
        status: reply.status ?? 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

const ok = (data: unknown): Reply => ({ body: { data } });
const fail = (status: number, message = "x"): Reply => ({
  status,
  body: { error: { code: status, message } },
});

const flush = () =>
  act(async () => {
    for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
  });

/**
 * `msPerSecond` shrinks the poll delay so a test can drive the loop through
 * several iterations in real time (1 means retryAfterSeconds: 5 waits 5ms).
 */
async function mount(msPerSecond?: number) {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <ChangeReceipt id={ID} msPerSecond={msPerSecond} />,
      host as HTMLElement,
    ),
  );
  await flush();
  return host;
}

const verificationCalls = () =>
  calls.filter((c) => c.url.endsWith("/verification"));

function badgeColors(root: HTMLElement): Map<string, string> {
  const out = new Map<string, string>();
  for (const span of root.querySelectorAll<HTMLElement>("span[style]")) {
    out.set(span.textContent ?? "", span.style.color);
  }
  return out;
}

test("applied with pending verification renders two separate badges", async () => {
  const verifying: VerificationView = {
    state: "verifying",
    checks: [],
    redactedChecks: 0,
    retryAfterSeconds: 5,
  };
  stubFetch(ok(receipt()), [ok(verifying)]);
  const root = await mount();

  const colors = badgeColors(root);
  expect(colors.has("Applied")).toBe(true);
  // The first poll has answered "verifying"; the two facts stay separate.
  expect(colors.has("Verifying")).toBe(true);
  expect(root.textContent).not.toContain("Applied · Verified");
  expect(verificationCalls()).toHaveLength(1);
});

test("before any poll answers, applied + pending reads as two badges", async () => {
  // A verification reply that never resolves keeps the stored "pending".
  calls = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, signal: init?.signal });
    if (url.endsWith("/verification")) return new Promise<Response>(() => {});
    return Promise.resolve(
      new Response(JSON.stringify({ data: receipt() }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
  const root = await mount();
  const colors = badgeColors(root);
  expect(colors.get("Applied")).toBe("var(--success)");
  expect(colors.get("Verification pending")).toBe("var(--text-muted)");
  expect(root.textContent).toContain("Checking the live objects");
});

test("polling stops at the first final state and re-reads the receipt", async () => {
  const done: VerificationView = {
    state: "verified",
    checks: [check({ status: "pass", reason: "ok" })],
    redactedChecks: 0,
  };
  stubFetch(ok(receipt()), [ok(done)]);
  const root = await mount();
  await flush();

  expect(verificationCalls()).toHaveLength(1);
  // Initial read + the settled re-read after the final verdict.
  expect(calls.filter((c) => c.url === `/api/v1/changes/${ID}`)).toHaveLength(
    2,
  );
  expect(badgeColors(root).get("Verified")).toBe("var(--success)");
  expect(root.textContent).not.toContain("Checking the live objects");
});

test("unmount aborts the verification request in flight", async () => {
  calls = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, signal: init?.signal });
    if (url.endsWith("/verification")) return new Promise<Response>(() => {});
    return Promise.resolve(
      new Response(JSON.stringify({ data: receipt() }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
  const root = await mount();
  const pending = verificationCalls()[0];
  expect(pending?.signal?.aborted).toBe(false);

  act(() => render(null, root));
  expect(pending?.signal?.aborted).toBe(true);
});

test("a final stored verdict is not polled at all", async () => {
  stubFetch(ok(receipt({ verification: { state: "verified", url: "" } })), [
    ok({ state: "verified", checks: [], redactedChecks: 0 }),
  ]);
  await mount();
  expect(verificationCalls()).toHaveLength(0);
});

test("inconclusive verification is neutral and spells out its reason", async () => {
  stubFetch(
    ok(
      receipt({
        verification: { state: "inconclusive", url: "" },
        checks: [check()],
      }),
    ),
  );
  const root = await mount();
  const colors = badgeColors(root);

  expect(colors.get("Verification inconclusive")).toBe("var(--text-muted)");
  expect(root.textContent).toContain("Why verification is inconclusive:");
  expect(root.textContent).toContain(
    "This kind has no supported readiness postcondition, so k8sCenter cannot verify it.",
  );
  for (const [label, color] of colors) {
    if (label.toLowerCase().includes("verif")) {
      expect(color).not.toBe("var(--success)");
    }
  }
});

test("a redacted object shows only its position", async () => {
  stubFetch(
    ok(
      receipt({
        state: "partial",
        verification: { state: "verified", url: "" },
        objects: [
          {
            index: 0,
            kind: "ConfigMap",
            namespace: "web",
            name: "cfg",
            action: "created",
          },
          { index: 1, redacted: true, reason: "forbidden" },
          { index: 2, redacted: true, reason: "forbidden" },
        ],
        redactedObjects: 2,
      }),
    ),
  );
  const root = await mount();
  const rows = [...root.querySelectorAll("tbody tr")];
  expect(rows).toHaveLength(3);
  expect(rows[1].textContent).toBe(
    "2Object #2 — you no longer have access to this resource",
  );
  expect(root.textContent).toContain(
    "2 objects are hidden because you no longer have access to them.",
  );
});

test("a redacted check shows only its status and reason", async () => {
  stubFetch(
    ok(
      receipt({
        verification: { state: "inconclusive", url: "" },
        checks: [
          {
            checkId: "postcondition",
            status: "inconclusive",
            severity: "info",
            reason: "read_forbidden",
            redacted: true,
            redactionReason: "forbidden",
            observedAt: "2026-10-01T10:00:01Z",
          },
        ],
        redactedChecks: 1,
      }),
    ),
  );
  const root = await mount();
  const list = root.querySelector('[aria-label="Verification checks"]');
  expect(list?.textContent).toContain(
    "Details hidden: you no longer have access to this resource",
  );
  expect(list?.textContent).toContain("You cannot read this object");
  expect(list?.textContent).not.toContain("web/");
});

test("a partial receipt offers Retry that carries only the receipt id and its cluster", async () => {
  stubFetch(
    ok(
      receipt({
        state: "partial",
        verification: { state: "verified", url: "" },
      }),
    ),
  );
  const root = await mount();
  const retry = [...root.querySelectorAll("a")].find(
    (a) => a.textContent === "Retry failed objects",
  );
  expect(retry?.getAttribute("href")).toBe(
    `/tools/yaml-apply?repairOf=${ID}&cluster=local`,
  );
});

test("a Secret-bearing failed receipt offers no reuse and shows no error text", async () => {
  stubFetch(
    ok(
      receipt({
        state: "failed",
        containsSecret: true,
        contentDigest: "",
        access: "admin",
        verification: { state: "inconclusive", url: "" },
        objects: [
          {
            index: 0,
            kind: "Secret",
            namespace: "web",
            name: "creds",
            action: "failed",
            errorClass: "forbidden",
          },
        ],
      }),
    ),
  );
  const root = await mount();
  expect(root.textContent).toContain(
    "The original content is not stored. Re-create the Secret manifest to retry.",
  );
  expect(
    [...root.querySelectorAll("a")].some((a) =>
      (a.getAttribute("href") ?? "").includes("repairOf"),
    ),
  ).toBe(false);
  expect(root.textContent).toContain("only its owner sees the digest");
  expect(root.querySelector(".text-danger")).toBeNull();
});

test("an unknown outcome explains itself and offers no retry", async () => {
  stubFetch(
    ok(
      receipt({
        state: "unknown",
        verification: { state: "inconclusive", url: "" },
      }),
    ),
  );
  const root = await mount();
  expect(badgeColors(root).get("Outcome unknown")).toBe("var(--warning)");
  expect(root.textContent).toContain("no one-click retry");
  expect(root.textContent).not.toContain("Retry failed objects");
  expect(root.querySelectorAll("button")).toHaveLength(0);
});

test("a changed cluster registration is called out", async () => {
  stubFetch(
    ok(
      receipt({
        targetGenerationChanged: true,
        verification: { state: "verified", url: "" },
      }),
    ),
  );
  const root = await mount();
  expect(root.textContent).toContain("The cluster registration changed");
});

test("no Git write affordance, even for a confirmed object with a repoURL", async () => {
  stubFetch(
    ok(
      receipt({
        verification: { state: "verified", url: "" },
        ownership: [
          {
            object: {
              clusterId: "local",
              group: "apps",
              kind: "Deployment",
              namespace: "web",
              name: "api",
            },
            controller: "argocd",
            confidence: "confirmed",
            reason: "confirmed-argo-status",
            apps: [
              {
                appId: "argo:argocd:shop",
                tool: "argocd",
                kind: "Application",
                namespace: "argocd",
                name: "shop",
                source: { repoURL: "https://git.example.com/shop.git" },
                suspended: false,
              },
            ],
            identityBasis: "group-kind-namespace-name",
            uidConfirmed: false,
            writableGitSource: false,
            observedAt: "2026-10-01T10:00:00Z",
          },
        ],
      }),
    ),
  );
  const root = await mount();
  const text = root.textContent ?? "";
  expect(text).toContain("Managed by Argo CD application argo:argocd:shop");
  expect(text.toLowerCase()).not.toContain("pull request");
  expect(text).not.toContain("git.example.com");
  expect(
    [...root.querySelectorAll("a")].map((a) => a.getAttribute("href")),
  ).not.toContain("https://git.example.com/shop.git");
});

test("load failures each say what happened", async () => {
  const seen: string[] = [];
  for (const status of [404, 503, 400]) {
    stubFetch(fail(status));
    const root = await mount();
    seen.push(root.querySelector('[role="alert"]')?.textContent ?? "");
    act(() => render(null, root));
    root.remove();
    host = null;
    if (originalFetch) globalThis.fetch = originalFetch;
  }
  expect(seen[0]).toContain("Change receipt not found");
  expect(seen[1]).toContain("Change records are unavailable");
  expect(seen[2]).toContain("not a valid change receipt id");
});

test("a 403 from verification keeps the stored verdict and explains the gate", async () => {
  stubFetch(ok(receipt({ clusterId: "remote-a" })), [fail(403)]);
  const root = await mount();
  expect(root.textContent).toContain(
    "Live verification needs access to this receipt's cluster",
  );
  expect(badgeColors(root).has("Verification pending")).toBe(true);
  expect(verificationCalls()).toHaveLength(1);
});

// --- Polling across iterations ----------------------------------------------
//
// These mount with msPerSecond = 1, so a retryAfterSeconds of N waits N ms of
// real time: the loop runs its real timers through several iterations.

/** Real time passes in small steps, with renders flushed, until `done`. */
async function until(done: () => boolean, limitMs = 500): Promise<void> {
  const deadline = Date.now() + limitMs;
  while (!done()) {
    if (Date.now() > deadline) throw new Error("condition never held");
    await act(async () => {
      await new Promise((r) => setTimeout(r, 2));
    });
  }
}

const settle = (ms = 40) =>
  act(async () => {
    await new Promise((r) => setTimeout(r, ms));
  });

const verifyingView = (retryAfterSeconds?: number): Reply =>
  ok({ state: "verifying", checks: [], redactedChecks: 0, retryAfterSeconds });

test("polling continues through verifying replies and stops at the final one", async () => {
  stubFetch(ok(receipt()), [
    verifyingView(2),
    verifyingView(2),
    ok({
      state: "verified",
      checks: [check({ status: "pass", reason: "ok" })],
      redactedChecks: 0,
    }),
  ]);
  const root = await mount(1);
  await until(() => verificationCalls().length >= 3);
  await settle();

  // Three passes, then nothing more: the loop ended at the final state.
  expect(verificationCalls()).toHaveLength(3);
  expect(badgeColors(root).get("Verified")).toBe("var(--success)");
  expect(root.textContent).not.toContain("Checking the live objects");
});

test("without retryAfterSeconds the default delay applies between passes", async () => {
  stubFetch(ok(receipt()), [verifyingView(undefined)]);
  await mount(1);
  // Default 5s at 1ms per second: a few passes in 60ms, not hundreds.
  await settle(60);
  const n = verificationCalls().length;
  expect(n).toBeGreaterThanOrEqual(2);
  expect(n).toBeLessThan(20);
});

test("unmount between passes stops polling for good", async () => {
  stubFetch(ok(receipt()), [verifyingView(5)]);
  const root = await mount(1);
  await until(() => verificationCalls().length >= 2);
  act(() => render(null, root));
  const after = verificationCalls().length;
  await settle(40);
  expect(verificationCalls()).toHaveLength(after);
});

test("an applying receipt is re-read until it settles, then verified", async () => {
  stubFetch(
    [
      ok(receipt({ state: "applying" })),
      ok(receipt({ state: "applying" })),
      ok(receipt({ state: "applied" })),
    ],
    [ok({ state: "inconclusive", checks: [check()], redactedChecks: 0 })],
  );
  const root = await mount(1);
  await until(() => verificationCalls().length >= 1);
  await settle();

  const reads = calls.map((c) =>
    c.url.endsWith("/verification") ? "verify" : "read",
  );
  // Three reads while applying settles, one verification pass, then the
  // settled re-read after the final verdict.
  expect(reads).toEqual(["read", "read", "read", "verify", "read"]);
  expect(badgeColors(root).has("Applied")).toBe(true);
});

for (const [status, text] of [
  [504, "Verification timed out."],
  [503, "Verification is unavailable right now"],
  [403, "Live verification needs access"],
] as const) {
  test(`a ${status} mid-polling stops the loop and explains itself`, async () => {
    stubFetch(ok(receipt()), [verifyingView(2), fail(status)]);
    const root = await mount(1);
    await until(() => verificationCalls().length >= 2);
    await settle();
    expect(verificationCalls()).toHaveLength(2);
    expect(root.textContent).toContain(text);
    expect(root.textContent).toContain(
      "Showing the last recorded verification",
    );
  });
}

test("Check again restarts the cycle and drops the stale live view", async () => {
  // First cycle: a live "verifying" view, then a 504. Meanwhile the stored
  // verdict is finalized elsewhere; the second cycle reads it and must show
  // it rather than the stale "verifying" view.
  stubFetch(
    [
      ok(receipt()),
      ok(
        receipt({
          verification: { state: "verification_failed", url: "" },
          checks: [check({ status: "fail", reason: "not_found" })],
        }),
      ),
    ],
    [verifyingView(1), fail(504)],
  );
  const root = await mount(1);
  await until(
    () => root.textContent?.includes("Verification timed out.") ?? false,
  );
  expect(badgeColors(root).has("Verifying")).toBe(true);

  const again = [...root.querySelectorAll("button")].find(
    (b) => b.textContent === "Check again",
  );
  if (!again) throw new Error("no Check again button");
  await act(async () => {
    again.click();
  });
  await settle();

  expect(calls.filter((c) => c.url === `/api/v1/changes/${ID}`)).toHaveLength(
    2,
  );
  const colors = badgeColors(root);
  expect(colors.has("Verifying")).toBe(false);
  expect(colors.get("Verification failed")).toBe("var(--error)");
  expect(root.textContent).not.toContain("Verification timed out.");
});

test("a malformed id is reported without calling the API", async () => {
  stubFetch(ok(receipt()));
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<ChangeReceipt id="%zz" />, host as HTMLElement));
  await flush();
  expect(host.querySelector('[role="alert"]')?.textContent).toContain(
    "This is not a valid change receipt id.",
  );
  expect(calls).toHaveLength(0);
});
