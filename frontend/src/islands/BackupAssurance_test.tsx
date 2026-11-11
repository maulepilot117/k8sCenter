import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import type {
  AssuranceException,
  AssurancePolicy,
  AssuranceStatus,
} from "@/lib/backup-assurance-types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setBrowser,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * Backup assurance page: what the status, exceptions and policies replies
 * render as, the Open/Resolved toggle and paging, the latest-request-wins
 * rule for each request stream, and the admin policy create / edit / delete
 * flows with their server errors. fetch is routed by method and URL.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: BackupAssurance } = await import("./BackupAssurance.tsx");

const STATUS = "/api/v1/velero/assurance/status";
const VELERO = "/api/v1/velero/status";
const EXCEPTIONS = "/api/v1/velero/assurance/exceptions";
const POLICIES = "/api/v1/velero/assurance/policies";

interface Call {
  method: string;
  url: string;
  body: unknown;
}

type Reply = Response | Promise<Response>;
type Route = (call: Call) => Reply | undefined;

let calls: Call[] = [];
let overrides: Route[] = [];

const reply = (status: number, payload: unknown): Response =>
  new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
const ok = (data: unknown, metadata?: unknown): Response =>
  reply(200, { data, metadata });
const fail = (
  status: number,
  reason: string | undefined,
  message = "boom",
  extra?: Record<string, unknown>,
): Response =>
  reply(status, { error: { code: status, message, reason, extra } });

function deferred() {
  let resolve!: (r: Response) => void;
  const promise = new Promise<Response>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function status(over: Partial<AssuranceStatus> = {}): AssuranceStatus {
  return {
    enabled: true,
    collection: "ok",
    collectionSource: "this_replica",
    policyCount: 2,
    open: {
      total: 2,
      byCondition: {
        overdue: 2,
        failed: 0,
        partially_failed: 0,
        paused: 0,
        never_run: 0,
        location_unavailable: 0,
        collection_unknown: 0,
      },
    },
    runtime: {
      holder: "replica-a",
      lastTickAt: null,
      lastRunAt: null,
      lastCollection: "ok",
      findingCount: 2,
      lastErrorAt: null,
      leaseHeld: true,
      lease: null,
      deliveryBacklog: { pending: 4, failed: 1 },
    },
    ...over,
  };
}

function exception(
  n: number,
  over: Partial<AssuranceException> = {},
): AssuranceException {
  return {
    id: `ex-${n}`,
    policyId: "pol-1",
    subject: {
      kind: "schedule",
      namespace: "velero",
      name: `sched-${n}`,
      uid: `u${n}`,
    },
    condition: "overdue",
    state: "open",
    severity: "critical",
    openedAt: "2026-01-01T00:00:00Z",
    lastObservedAt: "2026-01-02T00:00:00Z",
    resolvedAt: null,
    observationCount: 3,
    lastSuccessAt: null,
    subjectStatus: "found",
    detail: { expectedRunKnown: false, lastOutcome: "failure" },
    ...over,
  };
}

function policy(over: Partial<AssurancePolicy> = {}): AssurancePolicy {
  return {
    id: "pol-1",
    scopeKind: "schedule",
    scopeNamespace: "velero",
    scopeName: "nightly",
    maxAgeSeconds: 90000,
    graceSeconds: 3600,
    treatPartialAs: "failure",
    alertOnPaused: true,
    enabled: true,
    createdBy: "a",
    createdAt: "2026-01-01T00:00:00Z",
    updatedBy: "a",
    updatedAt: null,
    revision: 3,
    scheduleStatus: "found",
    ...over,
  };
}

const page = (from: number, n: number, state: "open" | "resolved" = "open") =>
  Array.from({ length: n }, (_, i) => exception(from + i, { state }));

/**
 * Installs the stub. A route in `overrides` answers first; otherwise the
 * defaults give an admin an ok collection, two open exceptions and one policy.
 */
function install() {
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const call: Call = {
      method,
      url,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    };
    calls.push(call);
    for (const route of overrides) {
      const r = route(call);
      if (r) return r;
    }
    if (url === STATUS) return ok(status());
    if (url === VELERO) return ok({ detected: true });
    if (url.startsWith(`${EXCEPTIONS}?`)) {
      return ok([exception(1), exception(2)], { total: 2 });
    }
    if (url === POLICIES && method === "GET") return ok([policy()]);
    if (method === "POST" || method === "PUT") return ok(policy());
    return fail(599, "unstubbed", url);
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  calls = [];
  overrides = [];
  setAccessToken("t");
  install();
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  setBrowser(true);
});

async function mount() {
  const view = render(<BackupAssurance />);
  await settle();
  return view.container as HTMLElement;
}

const click = async (el: Element) => {
  await act(async () => {
    fireEvent.click(el);
  });
  await settle();
};

const button = (name: string | RegExp) =>
  body().getByRole("button", { name }) as HTMLButtonElement;

const type = (el: HTMLElement, value: string) =>
  act(async () => {
    fireEvent.change(el, { target: { value } });
  });

const urls = (prefix: string) =>
  calls.filter((c) => c.method === "GET" && c.url.startsWith(prefix));
const exceptionUrls = () => urls(EXCEPTIONS).map((c) => c.url.split("?")[1]);
const q = (state: string, offset: number) =>
  `state=${state}&limit=50&offset=${offset}`;
const byId = (root: HTMLElement, id: string) =>
  root.querySelector(`[data-testid="${id}"]`);

test("an admin sees the banner, collection state, counts, collector, exceptions and policies", async () => {
  const root = await mount();
  const text = root.textContent ?? "";
  expect(byId(root, "assurance-honesty-banner")?.textContent).toContain(
    "does not verify that a restore would succeed",
  );
  expect(byId(root, "assurance-state-word")?.textContent).toBe("ok");
  expect(byId(root, "assurance-count-overdue")?.textContent).toContain("2");
  expect(byId(root, "assurance-runtime")).not.toBeNull();
  expect(text).toContain("4 pending, 1 failed");
  expect(
    root.querySelectorAll('[data-testid="assurance-exception"]'),
  ).toHaveLength(2);
  expect(text).toContain("schedule velero/sched-1");
  expect(
    root.querySelectorAll('[data-testid="assurance-policy-row"]'),
  ).toHaveLength(1);
  expect(text).toContain("schedule velero/nightly");
  expect(button("Add policy")).toBeDefined();
  expect(exceptionUrls()).toEqual([q("open", 0)]);
});

test("a non-admin gets no collector panel, no policy request and a read-only note", async () => {
  overrides.push(({ url }) =>
    url === STATUS
      ? ok(status({ runtime: undefined, policyCount: 2 }))
      : undefined,
  );
  const root = await mount();
  expect(byId(root, "assurance-runtime")).toBeNull();
  expect(urls(POLICIES)).toHaveLength(0);
  expect(byId(root, "assurance-policies-readonly")?.textContent).toContain(
    "2 policies apply",
  );
  expect(body().queryByRole("button", { name: "Add policy" })).toBeNull();
});

test("with no policies the page says nothing is evaluated and shows no counts", async () => {
  overrides.push(
    ({ url }) =>
      url === STATUS
        ? ok(status({ policyCount: 0, collection: "empty" }))
        : undefined,
    ({ url }) => (url === POLICIES ? ok([]) : undefined),
    ({ url }) =>
      url.startsWith(EXCEPTIONS) ? ok([], { total: 0 }) : undefined,
  );
  const root = await mount();
  expect(byId(root, "assurance-empty-policies")?.textContent).toContain(
    "No freshness policies configured",
  );
  expect(byId(root, "assurance-count-overdue")).toBeNull();
});

test("a zero-total open list reads as none while collection is ok", async () => {
  overrides.push(({ url }) =>
    url.startsWith(EXCEPTIONS) ? ok([], { total: 0 }) : undefined,
  );
  const root = await mount();
  expect(byId(root, "assurance-no-open")).not.toBeNull();
});

test("Velero not detected is called out as unknown state", async () => {
  overrides.push(({ url }) =>
    url === VELERO ? ok({ detected: false }) : undefined,
  );
  const root = await mount();
  expect(byId(root, "assurance-velero-not-detected")).not.toBeNull();
});

test("status failures: 403 forbidden, 503 unavailable, 501 remote, other error", async () => {
  const stateOf = (r: HTMLElement) =>
    byId(r, "assurance-surface-state")?.getAttribute("data-state");
  const cases: Array<[() => Response, (root: HTMLElement) => void]> = [
    [
      () => fail(403, undefined),
      (r) => {
        expect(stateOf(r)).toBe("forbidden");
        expect(byId(r, "assurance-error")).toBeNull();
      },
    ],
    [
      () => fail(503, "database_unavailable"),
      (r) => {
        expect(stateOf(r)).toBe("unavailable");
        expect(byId(r, "assurance-error")).toBeNull();
      },
    ],
    [
      () => fail(501, "remote_assurance_unsupported"),
      (r) =>
        expect(byId(r, "assurance-remote-unsupported")?.textContent).toContain(
          "Local cluster only",
        ),
    ],
    [
      () => fail(500, undefined, "kaput"),
      (r) =>
        expect(byId(r, "assurance-error")?.textContent).toContain(
          "could not be loaded: kaput",
        ),
    ],
  ];
  for (const [res, check] of cases) {
    calls = [];
    overrides = [({ url }) => (url === STATUS ? res() : undefined)];
    const view = render(<BackupAssurance />);
    await settle();
    const root = view.container as HTMLElement;
    check(root);
    // Nothing below a failed status is requested.
    expect(urls(POLICIES)).toHaveLength(0);
    expect(urls(EXCEPTIONS)).toHaveLength(0);
    expect(byId(root, "assurance-policies")).toBeNull();
    view.unmount();
  }
});

test("a failed exceptions load shows its own error and keeps the rest", async () => {
  overrides.push(({ url }) =>
    url.startsWith(EXCEPTIONS) ? fail(500, undefined, "list down") : undefined,
  );
  const root = await mount();
  expect(root.textContent).toContain(
    "Backup exceptions could not be loaded: list down.",
  );
  expect(
    root.querySelectorAll('[data-testid="assurance-policy-row"]'),
  ).toHaveLength(1);
});

test("a failed policies load shows its error", async () => {
  overrides.push(({ url }) =>
    url === POLICIES ? fail(500, undefined, "pol down") : undefined,
  );
  const root = await mount();
  expect(root.textContent).toContain("Policies could not be loaded: pol down");
});

test("Refresh reloads status, Velero, exceptions and policies", async () => {
  await mount();
  calls = [];
  await click(button("Refresh"));
  expect(calls.map((c) => c.url).sort()).toEqual(
    [STATUS, VELERO, `${EXCEPTIONS}?${q("open", 0)}`, POLICIES].sort(),
  );
});

test("the Open/Resolved toggle reloads from offset 0 and ignores the current state", async () => {
  const root = await mount();
  await click(button("Open"));
  expect(exceptionUrls()).toEqual([q("open", 0)]);

  await click(button("Resolved"));
  expect(exceptionUrls()).toEqual([q("open", 0), q("resolved", 0)]);
  expect(root.textContent).toContain("Resolved exceptions");
  await click(button("Open"));
  expect(exceptionUrls().at(-1)).toBe(q("open", 0));
});

test("paging moves by 50 and returns to the first page on a state change", async () => {
  overrides.push(({ url }) =>
    url.startsWith(EXCEPTIONS)
      ? ok(page(url.includes("offset=50") ? 51 : 1, 50), { total: 120 })
      : undefined,
  );
  const root = await mount();
  expect(root.textContent).toContain("1–50 of 120");
  await click(button("Next"));
  expect(exceptionUrls().at(-1)).toBe(q("open", 50));
  expect(root.textContent).toContain("51–100 of 120");
  await click(button("Previous"));
  expect(exceptionUrls().at(-1)).toBe(q("open", 0));

  await click(button("Next"));
  await click(button("Resolved"));
  expect(exceptionUrls().at(-1)).toBe(q("resolved", 0));
});

test("an emptied page reached from page one falls back to the last page", async () => {
  let shrunk = false;
  overrides.push(({ url }) => {
    if (!url.startsWith(EXCEPTIONS)) return undefined;
    if (url.includes("offset=50")) {
      shrunk = true;
      return ok([], { total: 40 });
    }
    return ok(page(1, 40), { total: shrunk ? 40 : 100 });
  });
  const root = await mount();
  await click(button("Next"));
  expect(exceptionUrls().slice(-2)).toEqual([q("open", 50), q("open", 0)]);
  expect(root.textContent).toContain("sched-40");
});

test("two toggles before a render both apply", async () => {
  const root = await mount();
  const resolved = button("Resolved");
  const open = button("Open");
  await act(async () => {
    resolved.click();
    open.click();
  });
  await settle();
  expect(exceptionUrls().slice(-2)).toEqual([q("resolved", 0), q("open", 0)]);
  expect(root.textContent).toContain("Open exceptions");
});

test("two Next presses before a render advance two pages", async () => {
  overrides.push(({ url }) =>
    url.startsWith(EXCEPTIONS) ? ok(page(1, 50), { total: 300 }) : undefined,
  );
  await mount();
  const next = button("Next");
  await act(async () => {
    next.click();
    next.click();
  });
  await settle();
  expect(exceptionUrls().slice(-2)).toEqual([q("open", 50), q("open", 100)]);
});

test("a page emptied by a shrinking list moves to the last page that exists", async () => {
  let shrunk = false;
  overrides.push(({ url }) => {
    if (!url.startsWith(EXCEPTIONS)) return undefined;
    if (url.includes("offset=100")) {
      shrunk = true;
      return ok([], { total: 60 });
    }
    return ok(page(shrunk ? 51 : 1, 50), { total: shrunk ? 60 : 150 });
  });
  const root = await mount();
  await click(button("Next"));
  await click(button("Next"));
  expect(exceptionUrls().slice(-3)).toEqual([
    q("open", 50),
    q("open", 100),
    q("open", 50),
  ]);
  expect(root.textContent).toContain("51–100 of 60");
});

test("a slow reply to an earlier load cannot overwrite a newer toggle", async () => {
  const slowOpen = deferred();
  let opens = 0;
  overrides.push(({ url }) => {
    if (!url.startsWith(EXCEPTIONS)) return undefined;
    if (url.includes("state=resolved")) {
      return ok([exception(9, { state: "resolved" })], { total: 1 });
    }
    opens++;
    return opens === 1 ? ok([exception(1)], { total: 1 }) : slowOpen.promise;
  });
  const root = await mount();
  // The Refresh's open list is slow; Resolved is requested and answers first.
  await click(button("Refresh"));
  await click(button("Resolved"));
  expect(root.textContent).toContain("sched-9");
  await act(async () => {
    slowOpen.resolve(ok([exception(7)], { total: 1 }));
  });
  await settle();
  expect(root.textContent).toContain("sched-9");
  expect(root.textContent).not.toContain("sched-7");
});

test("a slow status reply from an earlier load cannot overwrite a newer one", async () => {
  const slow = deferred();
  let n = 0;
  overrides.push(({ url }) => {
    if (url !== STATUS) return undefined;
    n++;
    if (n === 1) return ok(status());
    if (n === 2) return slow.promise;
    return ok(status({ collection: "stale" }));
  });
  const root = await mount();
  // The Refresh is still waiting on its status when a policy save reloads
  // the page and its reply arrives first.
  await click(button("Refresh"));
  await click(button("Add policy"));
  await type(body().getByLabelText("Namespace"), "velero");
  await type(body().getByLabelText("Schedule"), "weekly");
  await click(button("Create policy"));
  expect(byId(root, "assurance-state-word")?.textContent).toBe("stale");
  await act(async () => {
    slow.resolve(ok(status({ collection: "unknown" })));
  });
  await settle();
  expect(byId(root, "assurance-state-word")?.textContent).toBe("stale");
});

// --- Policy create -----------------------------------------------------------

test("creating a policy validates locally, posts the form and reloads", async () => {
  const root = await mount();
  await click(button("Add policy"));
  expect(byId(root, "assurance-policy-form")).not.toBeNull();

  // Empty namespace and schedule name: nothing is sent.
  await click(button("Create policy"));
  expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
  expect(root.textContent).toContain("is required for schedule scope");

  await type(body().getByLabelText("Namespace"), " velero ");
  await type(body().getByLabelText("Schedule"), " weekly ");
  await type(body().getByLabelText("Maximum age (minutes)"), "120");
  await type(body().getByLabelText("Grace (minutes)"), "5");
  await type(body().getByLabelText("Treat PartiallyFailed as"), "success");
  await act(async () => {
    fireEvent.click(
      body().getByLabelText(/Open an exception when the schedule is paused/),
    );
  });
  calls = [];
  await click(button("Create policy"));

  const post = calls.find((c) => c.method === "POST");
  expect(post?.url).toBe(POLICIES);
  expect(post?.body).toEqual({
    scopeKind: "schedule",
    scopeNamespace: "velero",
    scopeName: "weekly",
    maxAgeSeconds: 7200,
    graceSeconds: 300,
    treatPartialAs: "success",
    alertOnPaused: false,
    enabled: true,
  });
  expect(byId(root, "assurance-notice")?.textContent).toContain(
    "Policy created.",
  );
  expect(byId(root, "assurance-policy-form")).toBeNull();
  // The page reloaded.
  expect(calls.map((c) => c.url)).toContain(STATUS);
});

test("a cluster-scope policy sends no namespace or schedule name", async () => {
  await mount();
  await click(button("Add policy"));
  await type(body().getByLabelText("Scope"), "cluster");
  calls = [];
  await click(button("Create policy"));
  expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({
    scopeKind: "cluster",
    scopeNamespace: "",
    scopeName: "",
  });
});

async function fillValidCreate() {
  await click(button("Add policy"));
  await type(body().getByLabelText("Namespace"), "velero");
  await type(body().getByLabelText("Schedule"), "weekly");
}

test("a server invalid_policy places field errors on their fields", async () => {
  overrides.push(({ method }) =>
    method === "POST"
      ? fail(400, "invalid_policy", "bad", {
          fieldErrors: [
            { field: "maxAgeSeconds", message: "is too small" },
            { field: "mystery", message: "is odd" },
          ],
        })
      : undefined,
  );
  const root = await mount();
  await fillValidCreate();
  await click(button("Create policy"));
  expect(root.textContent).toContain("is too small");
  expect(byId(root, "assurance-policy-form-error")?.textContent).toContain(
    "mystery is odd",
  );
});

test("create errors: policy_exists and a 403 each say what to do", async () => {
  overrides.push(({ method }) =>
    method === "POST" ? fail(409, "policy_exists") : undefined,
  );
  const root = await mount();
  await fillValidCreate();
  await click(button("Create policy"));
  expect(byId(root, "assurance-policy-form-error")?.textContent).toContain(
    "A policy already exists for this scope.",
  );

  overrides = [
    ({ method }) => (method === "POST" ? fail(403, undefined) : undefined),
  ];
  await click(button("Create policy"));
  expect(byId(root, "assurance-policy-form-error")?.textContent).toContain(
    "Only administrators can change freshness policies.",
  );
});

// --- Policy edit -------------------------------------------------------------

test("editing a policy puts its values in the form and sends the revision", async () => {
  const root = await mount();
  await click(button("Edit"));
  expect(
    (body().getByLabelText("Maximum age (minutes)") as HTMLInputElement).value,
  ).toBe("1500");
  expect(
    (body().getByLabelText("Namespace") as HTMLInputElement).disabled,
  ).toBe(true);

  await type(body().getByLabelText("Maximum age (minutes)"), "60");
  calls = [];
  await click(button("Save changes"));
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.url).toBe(`${POLICIES}/pol-1`);
  expect(put?.body).toEqual({
    revision: 3,
    maxAgeSeconds: 3600,
    graceSeconds: 3600,
    treatPartialAs: "failure",
    alertOnPaused: true,
    enabled: true,
  });
  expect(byId(root, "assurance-notice")?.textContent).toBe("Policy saved.");
});

test("a revision conflict offers a reload that opens the fresh values", async () => {
  let saved = false;
  overrides.push(
    ({ method }) => {
      if (method !== "PUT") return undefined;
      saved = true;
      return fail(409, "revision_conflict");
    },
    ({ url, method }) =>
      url === POLICIES && method === "GET" && saved
        ? ok([policy({ revision: 4, maxAgeSeconds: 6000 })])
        : undefined,
  );
  const root = await mount();
  await click(button("Edit"));
  await click(button("Save changes"));
  expect(root.textContent).toContain("Someone else changed this policy");
  expect(button("Save changes").disabled).toBe(true);

  await click(button("Reload policy"));
  expect(
    (body().getByLabelText("Maximum age (minutes)") as HTMLInputElement).value,
  ).toBe("100");
  expect(byId(root, "assurance-notice")?.textContent).toContain(
    "Reloaded the current values.",
  );
  // The fresh policy is the one saved next.
  calls = [];
  await click(button("Save changes"));
  expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({
    revision: 4,
  });
});

test("reloading a policy that is gone closes the form", async () => {
  let saved = false;
  overrides.push(
    ({ method }) => {
      if (method !== "PUT") return undefined;
      saved = true;
      return fail(409, "revision_conflict");
    },
    ({ url, method }) =>
      url === POLICIES && method === "GET" && saved ? ok([]) : undefined,
  );
  const root = await mount();
  await click(button("Edit"));
  await click(button("Save changes"));
  await click(button("Reload policy"));
  expect(byId(root, "assurance-policy-form")).toBeNull();
  expect(byId(root, "assurance-notice")?.textContent).toBe(
    "That policy no longer exists.",
  );
});

test("Cancel closes the editor", async () => {
  const root = await mount();
  await click(button("Add policy"));
  await click(button("Cancel"));
  expect(byId(root, "assurance-policy-form")).toBeNull();
});

// --- Policy delete -----------------------------------------------------------

function confirmationRequired(open = 3): Route {
  return ({ method, url }) =>
    method === "DELETE" && !url.includes("confirm=true")
      ? fail(400, "confirmation_required", "confirm", { openExceptions: open })
      : undefined;
}

test("deleting asks the server first, then needs the typed name", async () => {
  overrides.push(confirmationRequired(3), ({ method, url }) =>
    method === "DELETE" && url.includes("confirm=true")
      ? ok({ id: "pol-1", deleted: true, discardedOpenExceptions: 2 })
      : undefined,
  );
  const root = await mount();
  await click(button("Delete"));
  expect(calls.filter((c) => c.method === "DELETE").map((c) => c.url)).toEqual([
    `${POLICIES}/pol-1`,
  ]);
  expect(body().getByRole("dialog").textContent).toContain(
    "3 open exceptions will be discarded.",
  );
  const confirm = button("Delete policy");
  expect(confirm.disabled).toBe(true);

  await type(body().getByPlaceholderText("nightly"), "nightly");
  expect(confirm.disabled).toBe(false);
  calls = [];
  await click(confirm);
  expect(calls.find((c) => c.method === "DELETE")?.url).toBe(
    `${POLICIES}/pol-1?confirm=true`,
  );
  expect(body().queryByRole("dialog")).toBeNull();
  expect(byId(root, "assurance-notice")?.textContent).toBe(
    "Policy deleted. 2 open exceptions were discarded with its history.",
  );
  expect(calls.map((c) => c.url)).toContain(STATUS);
});

test("a failed confirmed delete keeps the dialog and shows the error", async () => {
  overrides.push(confirmationRequired(1), ({ method, url }) =>
    method === "DELETE" && url.includes("confirm=true")
      ? fail(500, undefined, "cannot delete")
      : undefined,
  );
  await mount();
  await click(button("Delete"));
  expect(body().getByRole("dialog").textContent).toContain(
    "1 open exception will be discarded.",
  );
  await type(body().getByPlaceholderText("nightly"), "nightly");
  await click(button("Delete policy"));
  expect(body().getByRole("dialog").textContent).toContain("cannot delete");
  await click(button("Cancel"));
  expect(body().queryByRole("dialog")).toBeNull();
});

test("deleting a policy that is already gone says so and reloads the list", async () => {
  overrides.push(({ method }) =>
    method === "DELETE" ? fail(404, "policy_not_found") : undefined,
  );
  const root = await mount();
  calls = [];
  await click(button("Delete"));
  expect(byId(root, "assurance-notice")?.textContent).toBe(
    "That policy no longer exists.",
  );
  expect(urls(POLICIES)).toHaveLength(1);
});

test("the server-side placeholder is an empty root with the same attributes", async () => {
  setBrowser(false);
  const { container } = render(<BackupAssurance />);
  await settle();
  const root = container.firstElementChild as HTMLElement;
  expect(root.getAttribute("data-testid")).toBe("backup-assurance");
  expect(root.className).toBe("flex flex-col gap-5 p-6");
  expect(root.childElementCount).toBe(0);
  expect(calls).toHaveLength(0);
});
