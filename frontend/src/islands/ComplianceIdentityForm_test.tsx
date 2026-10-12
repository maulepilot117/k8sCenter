/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

GlobalRegistrator.register({ url: "http://localhost/" });

const toasts: Array<{ message: string; type: string }> = [];
mock.module("@/src/islands/ToastProvider.tsx", () => ({
  showToast: (message: string, type = "info") => {
    toasts.push({ message, type });
  },
}));

const { default: ComplianceIdentityForm } = await import(
  "./ComplianceIdentityForm.tsx"
);

afterAll(() => {
  GlobalRegistrator.unregister();
});

interface Call {
  method: string;
  body: Record<string, unknown> | null;
}

type Reply = { status: number; body?: unknown };

// putGate, when set, holds every PUT until it resolves, so a test can act
// while a save is in flight.
let putGate: Promise<void> | null = null;

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;
let calls: Call[] = [];
let replies: Record<string, Reply> = {};
let badges: Array<[string, string]> = [];

const IDENTITY = {
  username: "system:serviceaccount:kubecenter:compliance",
  groups: ["auditors", "ops"],
  revision: 1,
  updatedAt: "2026-10-10T00:00:00Z",
};

beforeEach(() => {
  toasts.length = 0;
  calls = [];
  replies = {};
  badges = [];
  putGate = null;
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    calls.push({
      method,
      body: init?.body ? JSON.parse(String(init.body)) : null,
    });
    if (method === "PUT" && putGate) await putGate;
    const reply = replies[method] ?? { status: 500, body: {} };
    if (reply.status === 204) return new Response(null, { status: 204 });
    return new Response(JSON.stringify(reply.body ?? {}), {
      status: reply.status,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
  setAccessToken("test-token");
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  setAccessToken(null);
});

async function settle() {
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

async function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <ComplianceIdentityForm
        clusterId="c1"
        onIdentity={(id, state) => badges.push([id, state])}
      />,
      host as HTMLElement,
    ),
  );
  await settle();
  return host;
}

/** The control a visible label names, through the label's `for`. */
function byLabel(root: HTMLElement, text: string): HTMLInputElement {
  const label = Array.from(root.querySelectorAll("label")).find(
    (l) => l.textContent?.trim() === text,
  );
  const id = label?.getAttribute("for");
  const el = id ? root.querySelector(`#${id}`) : label?.querySelector("input");
  if (!el) throw new Error(`no control labelled ${text}`);
  return el as HTMLInputElement;
}

function button(root: HTMLElement, label: string): HTMLButtonElement {
  const b = Array.from(root.querySelectorAll("button")).find(
    (el) => el.textContent?.trim() === label,
  );
  if (!b) throw new Error(`no button ${label}`);
  return b as HTMLButtonElement;
}

async function click(el: HTMLElement) {
  await act(async () => {
    el.click();
  });
  await settle();
}

async function type(el: HTMLInputElement, value: string) {
  await act(async () => {
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}

const USER = "Compliance identity username";
const GROUPS = "Compliance identity groups";

function puts() {
  return calls.filter((c) => c.method === "PUT");
}

function errReply(status: number, reason: string, message = "x") {
  return { status, body: { error: { code: status, message, reason } } };
}

test("a cluster with a stored identity fills the form and badges configured", async () => {
  replies.GET = { status: 200, body: { data: IDENTITY } };
  const root = await mount();
  expect(byLabel(root, USER).value).toBe(IDENTITY.username);
  expect(byLabel(root, GROUPS).value).toBe(
    ["auditors", "ops"].join(String.fromCharCode(10)),
  );
  expect(badges.at(-1)).toEqual(["c1", "configured"]);
  expect(root.textContent).toContain("taken as this identity");
});

test("a 404 compliance_history_not_configured badges not configured without an error toast", async () => {
  replies.GET = errReply(404, "compliance_history_not_configured");
  const root = await mount();
  expect(badges.at(-1)).toEqual(["c1", "not-configured"]);
  expect(toasts.length).toBe(0);
  expect(root.textContent).not.toContain("Clear identity");
});

test("saving sends the trimmed username and groups (lines or commas, blanks dropped) and badges configured", async () => {
  replies.GET = errReply(404, "compliance_history_not_configured");
  replies.PUT = { status: 200, body: { data: IDENTITY } };
  const root = await mount();
  await type(byLabel(root, USER), "  alice  ");
  await type(
    byLabel(root, GROUPS),
    ["auditors, ops", "", "  dev  ", ","].join(String.fromCharCode(10)),
  );
  await click(button(root, "Save"));
  expect(puts()[0].body).toEqual({
    username: "alice",
    groups: ["auditors", "ops", "dev"],
  });
  expect(badges.at(-1)).toEqual(["c1", "configured"]);
  expect(toasts.at(-1)?.type).toBe("success");
  expect(root.textContent).toContain("Clear identity");
});

test("an impersonation_denied answer shows the grant message and keeps the input", async () => {
  replies.GET = errReply(404, "compliance_history_not_configured");
  replies.PUT = errReply(422, "impersonation_denied");
  const root = await mount();
  await type(byLabel(root, USER), "alice");
  await type(byLabel(root, GROUPS), "auditors");
  await click(button(root, "Save"));
  expect(toasts.at(-1)?.type).toBe("error");
  expect(toasts.at(-1)?.message).toContain("impersonate");
  expect(byLabel(root, USER).value).toBe("alice");
  expect(byLabel(root, GROUPS).value).toBe("auditors");
  expect(badges.at(-1)).toEqual(["c1", "not-configured"]);
});

test("a cluster_unknown answer offers a reload", async () => {
  replies.GET = errReply(404, "compliance_history_not_configured");
  replies.PUT = errReply(404, "cluster_unknown");
  const root = await mount();
  await type(byLabel(root, USER), "alice");
  await click(button(root, "Save"));
  expect(toasts.at(-1)?.message).toContain("Reload");
});

test("a blank username cannot be saved", async () => {
  replies.GET = errReply(404, "compliance_history_not_configured");
  const root = await mount();
  expect(button(root, "Save").disabled).toBe(true);
  await type(byLabel(root, USER), "   ");
  expect(button(root, "Save").disabled).toBe(true);
  await type(byLabel(root, USER), "alice");
  expect(button(root, "Save").disabled).toBe(false);
  expect(puts().length).toBe(0);
});

test("clear asks for confirmation, sends DELETE, and badges not configured", async () => {
  replies.GET = { status: 200, body: { data: IDENTITY } };
  replies.DELETE = { status: 204 };
  let asked = "";
  globalThis.confirm = (m?: string) => {
    asked = m ?? "";
    return true;
  };
  const root = await mount();
  await click(button(root, "Clear identity"));
  expect(asked).toContain("Clear this compliance identity");
  expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  expect(badges.at(-1)).toEqual(["c1", "not-configured"]);
  expect(byLabel(root, USER).value).toBe("");
  expect(root.textContent).not.toContain("Clear identity");
});

test("declining the clear confirmation sends nothing", async () => {
  replies.GET = { status: 200, body: { data: IDENTITY } };
  globalThis.confirm = () => false;
  const root = await mount();
  await click(button(root, "Clear identity"));
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  expect(badges.at(-1)).toEqual(["c1", "configured"]);
});
