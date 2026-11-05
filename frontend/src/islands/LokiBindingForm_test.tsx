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

const { default: LokiBindingForm } = await import("./LokiBindingForm.tsx");

afterAll(() => {
  GlobalRegistrator.unregister();
});

interface Call {
  method: string;
  url: string;
  body: Record<string, unknown> | null;
}

type Reply = { status: number; body?: unknown };

// putGate, when set, holds every PUT until it resolves, so a test can act
// while a save is in flight. getGates holds the next GETs, one gate each.
// A request's reply is chosen when it is sent, so a held answer stays the
// one its cluster was given.
let putGate: Promise<void> | null = null;
let getGates: Array<Promise<void>> = [];
const originalConfirm = globalThis.confirm;

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;
let calls: Call[] = [];
let replies: Record<string, Reply> = {};
let badges: Array<[string, string]> = [];

const BINDING = {
  clusterId: "c1",
  url: "https://loki.example.com",
  tenantId: "team-a",
  hasToken: true,
  tokenStale: false,
  updatedAt: "2026-11-05T00:00:00Z",
};

const NOT_CONFIGURED: Reply = {
  status: 404,
  body: {
    error: {
      code: 404,
      message: "a Loki URL is not configured for the selected cluster",
      reason: "loki_not_configured",
    },
  },
};

beforeEach(() => {
  toasts.length = 0;
  calls = [];
  replies = {};
  badges = [];
  putGate = null;
  getGates = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    calls.push({
      method,
      url: String(input),
      body: init?.body ? JSON.parse(String(init.body)) : null,
    });
    const reply = replies[method] ?? { status: 500, body: {} };
    if (method === "PUT" && putGate) await putGate;
    const gate = method === "GET" ? getGates.shift() : undefined;
    if (gate) await gate;
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
  globalThis.confirm = originalConfirm;
  setAccessToken(null);
});

async function settle() {
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

function renderForm(clusterId: string) {
  act(() =>
    render(
      <LokiBindingForm
        clusterId={clusterId}
        onBinding={(id, state) => badges.push([id, state])}
      />,
      host as HTMLElement,
    ),
  );
}

async function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  renderForm("c1");
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

const URL_FIELD = "Loki URL";
const TOKEN = "Bearer token (optional)";
const TENANT = "Tenant ID (optional)";
const CLEAR = "Clear the stored token on save";
const REENTRY = "A different Loki host or port needs its token entered again";

function puts() {
  return calls.filter((c) => c.method === "PUT");
}

test("a stored binding fills the URL and tenant but never the token", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();
  expect(calls[0].url).toContain("/v1/clusters/c1/logs");
  expect(byLabel(root, URL_FIELD).value).toBe("https://loki.example.com");
  expect(byLabel(root, TENANT).value).toBe("team-a");
  const token = byLabel(root, TOKEN);
  expect(token.type).toBe("password");
  expect(token.value).toBe("");
  expect(root.textContent).toContain("A token is stored for this binding.");
  expect(badges).toEqual([["c1", "configured"]]);
});

test("an unconfigured cluster reports not-configured without an error toast", async () => {
  replies.GET = NOT_CONFIGURED;
  const root = await mount();
  expect(badges).toEqual([["c1", "not-configured"]]);
  expect(toasts).toEqual([]);
  expect(byLabel(root, URL_FIELD).value).toBe("");
  expect(button(root, "Save").disabled).toBe(true);
  expect(() => button(root, "Remove binding")).toThrow();
});

test("an untouched token is omitted, and the current tenant is always sent", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await click(button(root, "Save"));
  expect(puts()[0].body).toEqual({
    url: "https://loki.example.com",
    tenantId: "team-a",
  });
});

test("a typed token is sent as token", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await type(byLabel(root, TOKEN), "loki-secret");
  await click(button(root, "Save"));
  expect(puts()[0].body?.token).toBe("loki-secret");
  // The saved answer resets the field: the token is never shown back.
  expect(byLabel(root, TOKEN).value).toBe("");
});

test("the clear box sends an empty token", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = {
    status: 200,
    body: { data: { ...BINDING, hasToken: false } },
  };
  const root = await mount();
  await click(byLabel(root, CLEAR));
  await click(button(root, "Save"));
  expect(puts()[0].body?.token).toBe("");
});

test("a cleared tenant is sent as an empty tenantId", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await type(byLabel(root, TENANT), "");
  await click(button(root, "Save"));
  expect(puts()[0].body?.tenantId).toBe("");
});

test("moving the URL to another host requires the token before Save is enabled", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();

  // Same host, another path: the stored token still applies.
  await type(byLabel(root, URL_FIELD), "https://loki.example.com/gateway");
  expect(root.textContent).not.toContain(REENTRY);
  expect(button(root, "Save").disabled).toBe(false);

  await type(byLabel(root, URL_FIELD), "https://other.example.com");
  expect(root.textContent).toContain(REENTRY);
  expect(byLabel(root, TOKEN).getAttribute("aria-invalid")).toBe("true");
  expect(button(root, "Save").disabled).toBe(true);

  await type(byLabel(root, TOKEN), "new-secret");
  expect(button(root, "Save").disabled).toBe(false);
  await click(button(root, "Save"));
  expect(puts()[0].body?.token).toBe("new-secret");
});

test("a stale token prompts for entry, and clearing it is enough to save", async () => {
  replies.GET = {
    status: 200,
    body: { data: { ...BINDING, hasToken: false, tokenStale: true } },
  };
  replies.PUT = {
    status: 200,
    body: { data: { ...BINDING, hasToken: false, tokenStale: false } },
  };
  const root = await mount();
  expect(root.textContent).toContain(REENTRY);
  expect(button(root, "Save").disabled).toBe(true);
  await click(byLabel(root, CLEAR));
  expect(button(root, "Save").disabled).toBe(false);
  await click(button(root, "Save"));
  expect(puts()[0].body?.token).toBe("");
});

test("the server's 400 and 409 messages are shown as sent", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();

  const tenant =
    "tenantId may hold at most 150 letters, digits and the characters !-_.*'() and | between tenants";
  replies.PUT = {
    status: 400,
    body: { error: { code: 400, message: tenant } },
  };
  await click(button(root, "Save"));
  expect(toasts.at(-1)).toEqual({ message: tenant, type: "error" });

  const conflict =
    "the Loki binding was changed by another save; reload it and try again";
  replies.PUT = {
    status: 409,
    body: { error: { code: 409, message: conflict } },
  };
  await click(button(root, "Save"));
  expect(toasts.at(-1)).toEqual({ message: conflict, type: "error" });
});

test("an unexpected failure shows the fallback, not the server text", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = {
    status: 500,
    body: { error: { code: 500, message: "internal detail" } },
  };
  const root = await mount();
  await click(button(root, "Save"));
  expect(toasts.at(-1)).toEqual({
    message: "Failed to save the Loki binding",
    type: "error",
  });
});

test("removing the binding confirms, deletes and reports not-configured", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.DELETE = { status: 204 };
  let asked = "";
  globalThis.confirm = (m?: string) => {
    asked = m ?? "";
    return true;
  };
  const root = await mount();
  await click(button(root, "Remove binding"));
  expect(asked).toContain("token and tenant");
  expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  expect(badges.at(-1)).toEqual(["c1", "not-configured"]);
  expect(byLabel(root, URL_FIELD).value).toBe("");
  expect(byLabel(root, TENANT).value).toBe("");
});

test("a save that finishes after the form moved to another cluster badges its own cluster and leaves the new form alone", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();

  let release = () => {};
  putGate = new Promise<void>((r) => {
    release = r;
  });
  replies.PUT = {
    status: 200,
    body: { data: { ...BINDING, url: "https://saved-c1.example.com" } },
  };
  await click(button(root, "Save"));

  // The admin opens another cluster's binding while c1's save is in flight.
  replies.GET = {
    status: 200,
    body: {
      data: {
        ...BINDING,
        clusterId: "c2",
        url: "https://loki-c2.example.com",
        tenantId: "",
        hasToken: false,
      },
    },
  };
  renderForm("c2");
  await settle();
  expect(byLabel(root, URL_FIELD).value).toBe("https://loki-c2.example.com");

  toasts.length = 0;
  const before = badges.length;
  release();
  await settle();

  // The late save still updates c1's badge, and only c1's.
  expect(badges.slice(before)).toEqual([["c1", "configured"]]);
  expect(byLabel(root, URL_FIELD).value).toBe("https://loki-c2.example.com");
  expect(toasts).toEqual([]);
  expect(button(root, "Save").disabled).toBe(false);
});

test("adding an explicit default port counts as a move, as it does on the server", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await type(byLabel(root, URL_FIELD), "https://loki.example.com:443");
  expect(root.textContent).toContain(REENTRY);
  expect(button(root, "Save").disabled).toBe(true);
});

test("a typed token wins over a ticked clear box", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await click(byLabel(root, CLEAR));
  await type(byLabel(root, TOKEN), "fresh-secret");
  await click(button(root, "Save"));
  expect(puts()[0].body?.token).toBe("fresh-secret");
});

test("the token field asks password managers not to fill a saved password", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();
  expect(byLabel(root, TOKEN).getAttribute("autocomplete")).toBe(
    "new-password",
  );
  expect(byLabel(root, URL_FIELD).getAttribute("autocomplete")).toBe("off");
  expect(byLabel(root, TENANT).getAttribute("autocomplete")).toBe("off");
});

test("a failed load keeps Save off, so a save cannot replace a tenant the form never saw", async () => {
  replies.GET = {
    status: 503,
    body: {
      error: {
        code: 503,
        message: "the Loki binding could not be read or saved",
      },
    },
  };
  const root = await mount();
  expect(toasts.at(-1)).toEqual({
    message: "the Loki binding could not be read or saved",
    type: "error",
  });
  expect(badges).toEqual([]);
  expect(root.textContent).toContain("could not be loaded");
  await type(byLabel(root, URL_FIELD), "https://loki.example.com");
  expect(button(root, "Save").disabled).toBe(true);
});

test("an unexpected load failure shows the fallback, not the server text", async () => {
  replies.GET = {
    status: 500,
    body: { error: { code: 500, message: "internal detail" } },
  };
  await mount();
  expect(toasts.at(-1)).toEqual({
    message: "Could not load the Loki binding",
    type: "error",
  });
});

test("a load that answers after the form moved to another cluster leaves the new form alone", async () => {
  let releaseFirst = () => {};
  getGates = [
    new Promise<void>((r) => {
      releaseFirst = r;
    }),
  ];
  replies.GET = { status: 200, body: { data: BINDING } };
  host = document.createElement("div");
  document.body.appendChild(host);
  renderForm("c1");
  await settle();

  replies.GET = {
    status: 200,
    body: {
      data: {
        ...BINDING,
        clusterId: "c2",
        url: "https://loki-c2.example.com",
        tenantId: "team-c2",
      },
    },
  };
  renderForm("c2");
  await settle();
  expect(byLabel(host, URL_FIELD).value).toBe("https://loki-c2.example.com");

  releaseFirst();
  await settle();
  expect(byLabel(host, URL_FIELD).value).toBe("https://loki-c2.example.com");
  expect(byLabel(host, TENANT).value).toBe("team-c2");
  // Only c2's load reported: c1's answer arrived for a form no longer shown.
  expect(badges).toEqual([["c2", "configured"]]);
});

test("declining the remove confirmation sends nothing", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  globalThis.confirm = () => false;
  const root = await mount();
  await click(button(root, "Remove binding"));
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  expect(byLabel(root, URL_FIELD).value).toBe("https://loki.example.com");
});

test("a binding another admin already removed is reported as removed", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.DELETE = NOT_CONFIGURED;
  globalThis.confirm = () => true;
  const root = await mount();
  await click(button(root, "Remove binding"));
  expect(badges.at(-1)).toEqual(["c1", "not-configured"]);
  expect(toasts.at(-1)).toEqual({
    message: "Loki binding removed",
    type: "success",
  });
  expect(() => button(root, "Remove binding")).toThrow();
});

test("a failed remove keeps the form and its badge as they were", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.DELETE = {
    status: 503,
    body: {
      error: {
        code: 503,
        message: "the Loki binding could not be read or saved",
      },
    },
  };
  globalThis.confirm = () => true;
  const root = await mount();
  await click(button(root, "Remove binding"));
  expect(toasts.at(-1)).toEqual({
    message: "the Loki binding could not be read or saved",
    type: "error",
  });
  expect(badges).toEqual([["c1", "configured"]]);
  expect(byLabel(root, URL_FIELD).value).toBe("https://loki.example.com");
  expect(button(root, "Remove binding").disabled).toBe(false);
});
