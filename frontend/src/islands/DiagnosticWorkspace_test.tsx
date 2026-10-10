/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * The diagnostics API refuses a remote cluster selection with 501
 * unsupported_platform (#532): the target, its pods and the blast radius are
 * resolved from the local cluster's informers. The workspace must say so
 * plainly rather than show the refusal as an error.
 *
 * The island only runs its mount effect when `IS_BROWSER` is true, and that
 * constant is fixed when `is-browser.ts` is first evaluated, which under
 * `bun test` is without a DOM. As in source-coverage-pages_test.tsx, this
 * file stubs it to true for its own island and puts the real value back
 * afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: DiagnosticWorkspace } = await import(
  "./DiagnosticWorkspace.tsx"
);
const { fetchCurrentUser, logout } = await import("@/lib/auth.ts");

/**
 * Signs a user in, as the top bar's /auth/me load does: the capture button
 * stays inactive until the signed-in user is known.
 */
async function signIn() {
  const previous = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response(
      JSON.stringify({
        data: {
          user: {
            id: "u1",
            username: "u1",
            provider: "local",
            kubernetesUsername: "u1",
            kubernetesGroups: [],
            roles: [],
          },
          rbac: {},
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    )) as unknown as typeof globalThis.fetch;
  try {
    await fetchCurrentUser();
  } finally {
    globalThis.fetch = previous;
  }
}

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

/** Signs out (logout() with its request answered), resetting the shared user. */
async function signOut() {
  const previous = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response("{}", {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })) as unknown as typeof globalThis.fetch;
  try {
    await logout();
  } finally {
    globalThis.fetch = previous;
  }
}

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;

afterEach(async () => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
  globalThis.history.replaceState(null, "", "/");
  // Unmounted first, then signed out: the signed-in user is a module-wide
  // signal, never left behind for the next file.
  await signOut();
});

function stubFetch(status: number, body: unknown) {
  originalFetch = globalThis.fetch;
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    )) as unknown as typeof globalThis.fetch;
}

/** Mounts the workspace with a full target in the URL, so it runs at once. */
async function mount(status: number, body: unknown) {
  stubFetch(status, body);
  setAccessToken("test-token");
  globalThis.history.replaceState(
    null,
    "",
    "/?namespace=team-a&kind=Pod&name=web",
  );
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<DiagnosticWorkspace />, host as HTMLElement));
  // The fetch, the body read and the state update each take a turn of the
  // event loop; settle until the running state is gone (bounded).
  for (let i = 0; i < 20; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
    if (!host.textContent?.includes("Running diagnostics")) break;
  }
  return host;
}

const NOTICE = "not available for remote clusters";
const REFUSAL = "resource diagnostics are available for the local cluster only";

test("a remote refusal renders the unsupported notice, not an error", async () => {
  const root = await mount(501, {
    error: { code: 501, message: REFUSAL, reason: "unsupported_platform" },
  });
  expect(
    root.querySelector('[data-diagnostics-state="remote-unsupported"]'),
  ).not.toBeNull();
  expect(root.textContent).toContain(NOTICE);
  expect(root.textContent).not.toContain(REFUSAL);
});

test("any other failure still renders the error message", async () => {
  const root = await mount(500, {
    error: { code: 500, message: "failed to resolve resource" },
  });
  expect(root.textContent).toContain("failed to resolve resource");
  expect(root.textContent).not.toContain(NOTICE);
});

test("a local result renders without the remote notice", async () => {
  const root = await mount(200, {
    data: {
      target: { kind: "Pod", name: "web", namespace: "team-a" },
      results: [],
      blastRadius: { directlyAffected: [], potentiallyAffected: [] },
    },
  });
  expect(root.textContent).not.toContain(NOTICE);
  expect(
    root.querySelector('[data-diagnostics-state="remote-unsupported"]'),
  ).toBeNull();
  expect(root.textContent).not.toContain("Select a resource to investigate");
});

test("a local result offers capture to an incident beside Re-scan", async () => {
  await signIn();
  const root = await mount(200, {
    data: {
      target: { kind: "Pod", name: "web", namespace: "team-a" },
      results: [],
      blastRadius: { directlyAffected: [], potentiallyAffected: [] },
    },
  });
  const capture = root.querySelector('[data-testid="capture-to-incident"]');
  expect(capture).not.toBeNull();
  expect(capture?.getAttribute("aria-disabled")).toBe("false");
  const banner = capture?.parentElement?.parentElement;
  expect(banner?.textContent).toContain("Re-scan");
});

test("each picker field is named by its visible label", () => {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<DiagnosticWorkspace />, host as HTMLElement));
  const labels = Array.from(host.querySelectorAll("label"));
  expect(labels.map((l) => l.textContent)).toEqual([
    "Namespace",
    "Kind",
    "Name",
  ]);
  for (const label of labels) {
    const field = label.htmlFor
      ? host.querySelector(`#${CSS.escape(label.htmlFor)}`)
      : null;
    expect(field?.tagName).toMatch(/^(INPUT|SELECT)$/);
  }
});

function check(
  status: "pass" | "warn" | "fail",
  severity: "critical" | "warning" | "info",
) {
  return { ruleName: `${status}-${severity}`, status, severity, message: "m" };
}

for (const { name, results, text, banner, tone } of [
  {
    name: "a critical failure",
    results: [check("fail", "critical"), check("warn", "warning")],
    text: "1 critical issue",
    banner: "bg-error-dim",
    tone: "text-error",
  },
  {
    name: "warnings only",
    results: [check("warn", "warning"), check("fail", "warning")],
    text: "2 warnings",
    banner: "bg-warning-dim",
    tone: "text-warning",
  },
  {
    name: "no failures",
    results: [check("pass", "info")],
    text: "All checks passed",
    banner: "bg-success-dim",
    tone: "text-success",
  },
]) {
  test(`the status banner takes the ${tone} tone for ${name}`, async () => {
    const root = await mount(200, {
      data: {
        target: { kind: "Pod", name: "web", namespace: "team-a" },
        results,
        blastRadius: { directlyAffected: [], potentiallyAffected: [] },
      },
    });
    const status = Array.from(root.querySelectorAll("span")).find(
      (s) => s.textContent === text,
    );
    expect(status?.classList.contains(tone)).toBe(true);
    const bar = status?.parentElement?.parentElement;
    expect(bar?.classList.contains(banner)).toBe(true);
  });
}

const TRUNCATED_MESSAGE =
  "more than 5000 pods in this namespace on the selected cluster; they are left out of this graph";

test("a truncated blast radius says it is incomplete and names each kind", async () => {
  const root = await mount(200, {
    data: {
      target: { kind: "Pod", name: "web", namespace: "team-a" },
      results: [],
      blastRadius: {
        directlyAffected: [],
        potentiallyAffected: [],
        truncated: true,
        errors: { pods: TRUNCATED_MESSAGE },
      },
    },
  });
  const notice = root.querySelector('[data-testid="blast-radius-truncated"]');
  expect(notice).not.toBeNull();
  expect(notice?.textContent).toContain("Blast radius is incomplete");
  expect(notice?.textContent).toContain(TRUNCATED_MESSAGE);
});

test("a node-capped blast radius with no read errors says the graph was capped", async () => {
  const root = await mount(200, {
    data: {
      target: { kind: "Pod", name: "web", namespace: "team-a" },
      results: [],
      blastRadius: {
        directlyAffected: [],
        potentiallyAffected: [],
        truncated: true,
      },
    },
  });
  const notice = root.querySelector('[data-testid="blast-radius-truncated"]');
  expect(notice).not.toBeNull();
  expect(notice?.textContent).toContain("Blast radius is incomplete");
  expect(notice?.textContent).toContain("the namespace graph was capped");
  expect(notice?.textContent).not.toContain("could not be read");
});

test("a complete blast radius renders no truncation notice", async () => {
  const root = await mount(200, {
    data: {
      target: { kind: "Pod", name: "web", namespace: "team-a" },
      results: [],
      blastRadius: { directlyAffected: [], potentiallyAffected: [] },
    },
  });
  expect(
    root.querySelector('[data-testid="blast-radius-truncated"]'),
  ).toBeNull();
  expect(root.textContent).not.toContain("Blast radius is incomplete");
});
