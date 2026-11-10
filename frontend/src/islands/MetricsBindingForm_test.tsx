/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const toasts: Array<{ message: string; type: string }> = [];
// bun shares modules across test files, so the real module is kept and put
// back in afterAll; ToastProvider_test.tsx uses the real one.
const realToastStore = {
  ...(await import("@/src/lib/toast-store.ts")),
};
mock.module("@/src/lib/toast-store.ts", () => ({
  ...realToastStore,
  showToast: (message: string, type = "info") => {
    toasts.push({ message, type });
  },
}));

const { default: MetricsBindingForm } = await import(
  "./MetricsBindingForm.tsx"
);

afterAll(() => {
  mock.module("@/src/lib/toast-store.ts", () => realToastStore);
});

interface Call {
  method: string;
  body: Record<string, unknown> | null;
}

type Reply = { status: number; body?: unknown };

// putGate, when set, holds every PUT until it resolves, so a test can act
// while a save is in flight.
let putGate: Promise<void> | null = null;
let deleteGate: Promise<void> | null = null;
let getGates: Array<Promise<void>> = [];

let view: ReturnType<typeof render> | null = null;
const originalConfirm = globalThis.confirm;
let originalFetch: typeof globalThis.fetch | undefined;
let calls: Call[] = [];
let replies: Record<string, Reply> = {};
let badges: Array<[string, string]> = [];

const BINDING = {
  clusterId: "c1",
  prometheusUrl: "https://prom.example.com",
  hasToken: true,
  alertmanagerUrl: "https://am.example.com",
  hasAlertmanagerToken: true,
  alertmanagerTokenStale: false,
  updatedAt: "2026-10-10T00:00:00Z",
};

beforeEach(() => {
  toasts.length = 0;
  calls = [];
  replies = {};
  badges = [];
  putGate = null;
  deleteGate = null;
  getGates = [];
  view = null;
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    calls.push({
      method,
      body: init?.body ? JSON.parse(String(init.body)) : null,
    });
    if (method === "PUT" && putGate) await putGate;
    if (method === "DELETE" && deleteGate) await deleteGate;
    const gate = method === "GET" ? getGates.shift() : undefined;
    if (gate) await gate;
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
  if (originalFetch) globalThis.fetch = originalFetch;
  globalThis.confirm = originalConfirm;
  setAccessToken(null);
});

function form(clusterId: string) {
  return (
    <MetricsBindingForm
      clusterId={clusterId}
      onBinding={(id, state) => badges.push([id, state])}
    />
  );
}

function renderForm(clusterId: string) {
  if (view) view.rerender(form(clusterId));
  else view = render(form(clusterId));
}

async function mount() {
  renderForm("c1");
  await settle();
  return (view as ReturnType<typeof render>).container;
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
    fireEvent.input(el, { target: { value } });
  });
  await settle();
}

const AM_TOKEN = "Alertmanager bearer token (optional)";
const PROM_TOKEN = "Prometheus bearer token (optional)";
const AM_URL = "Alertmanager URL (optional)";
const REENTRY = "A different Alertmanager host needs its token entered again";

function puts() {
  return calls.filter((c) => c.method === "PUT");
}

test("the Prometheus and Alertmanager token fields are found by their labels", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();
  expect(byLabel(root, PROM_TOKEN).type).toBe("password");
  expect(byLabel(root, AM_TOKEN).type).toBe("password");
  expect(byLabel(root, PROM_TOKEN)).not.toBe(byLabel(root, AM_TOKEN));
  expect(root.textContent).toContain("entered in both fields");
});

test("an Alertmanager token typed in the field is sent as alertmanagerToken", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await type(byLabel(root, AM_TOKEN), "am-secret");
  await click(button(root, "Save"));
  expect(puts()[0].body).toEqual({
    prometheusUrl: "https://prom.example.com",
    alertmanagerUrl: "https://am.example.com",
    alertmanagerToken: "am-secret",
  });
});

test("a blank Alertmanager token is omitted from the body", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await click(button(root, "Save"));
  const body = puts()[0].body as Record<string, unknown>;
  expect("alertmanagerToken" in body).toBe(false);
  expect("token" in body).toBe(false);
});

test("the Alertmanager clear box sends an empty alertmanagerToken and leaves the Prometheus token alone", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  expect(root.textContent).toContain("An Alertmanager token is stored");
  await click(byLabel(root, "Clear the stored Alertmanager token on save"));
  await click(button(root, "Save"));
  const body = puts()[0].body as Record<string, unknown>;
  expect(body.alertmanagerToken).toBe("");
  expect("token" in body).toBe(false);
});

test("editing the Alertmanager URL to another host requires the token before Save is enabled", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  expect(root.textContent).not.toContain(REENTRY);

  // The same host on another path is not a move.
  await type(byLabel(root, AM_URL), "https://am.example.com/other");
  expect(root.textContent).not.toContain(REENTRY);

  await type(byLabel(root, AM_URL), "https://am2.example.com");
  expect(root.textContent).toContain(REENTRY);
  expect(root.textContent).not.toContain("An Alertmanager token is stored");
  const field = byLabel(root, AM_TOKEN);
  expect(field.required).toBe(true);
  const describedBy = field.getAttribute("aria-describedby") as string;
  expect(root.querySelector(`#${describedBy}`)?.textContent).toContain(REENTRY);
  expect(button(root, "Save").disabled).toBe(true);

  await type(field, "new-am-token");
  expect(button(root, "Save").disabled).toBe(false);
  await click(button(root, "Save"));
  expect(puts()[0].body).toMatchObject({
    alertmanagerUrl: "https://am2.example.com",
    alertmanagerToken: "new-am-token",
  });
});

test("a stale Alertmanager token shows the re-entry message and the clear box, and clearing sends an empty token", async () => {
  replies.GET = {
    status: 200,
    // The store's real stale shape: the token is withheld, so
    // hasAlertmanagerToken is false while alertmanagerTokenStale is true.
    body: {
      data: {
        ...BINDING,
        hasAlertmanagerToken: false,
        alertmanagerTokenStale: true,
      },
    },
  };
  replies.PUT = {
    status: 200,
    body: { data: { ...BINDING, hasAlertmanagerToken: false } },
  };
  const root = await mount();
  expect(root.textContent).toContain(REENTRY);
  expect(root.textContent).not.toContain("An Alertmanager token is stored");
  expect(button(root, "Save").disabled).toBe(true);

  await click(byLabel(root, "Clear the stored Alertmanager token on save"));
  expect(button(root, "Save").disabled).toBe(false);
  await click(button(root, "Save"));
  expect((puts()[0].body as Record<string, unknown>).alertmanagerToken).toBe(
    "",
  );
});

test("a stale Alertmanager token stops prompting once the Alertmanager URL is removed", async () => {
  replies.GET = {
    status: 200,
    body: {
      data: {
        ...BINDING,
        hasAlertmanagerToken: false,
        alertmanagerTokenStale: true,
      },
    },
  };
  const root = await mount();
  expect(button(root, "Save").disabled).toBe(true);

  await type(byLabel(root, AM_URL), "");
  expect(root.textContent).not.toContain(REENTRY);
  expect(button(root, "Save").disabled).toBe(false);
});

test("the server's re-entry 400 and its 409 are shown as sent", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();

  const reentry =
    "the Alertmanager token must be re-entered when the Alertmanager URL changes host";
  replies.PUT = {
    status: 400,
    body: { error: { code: 400, message: reentry } },
  };
  await click(button(root, "Save"));
  expect(toasts.at(-1)).toEqual({ message: reentry, type: "error" });

  const conflict =
    "the metrics binding was changed by another save; reload it and try again";
  replies.PUT = {
    status: 409,
    body: { error: { code: 409, message: conflict } },
  };
  await click(button(root, "Save"));
  expect(toasts.at(-1)).toEqual({ message: conflict, type: "error" });
});

test("an unreachable Alertmanager shows a fixed sentence", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = {
    status: 502,
    body: {
      error: {
        code: 502,
        message: "could not reach Alertmanager at the given URL",
        reason: "unreachable",
      },
    },
  };
  const root = await mount();
  await click(button(root, "Save"));
  expect(toasts.at(-1)?.message).toBe(
    "Could not reach Alertmanager at that URL",
  );
});

test("the remove confirmation names the Alertmanager URL and token", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.DELETE = { status: 204 };
  let asked = "";
  globalThis.confirm = (m?: string) => {
    asked = m ?? "";
    return true;
  };
  const root = await mount();
  await click(button(root, "Remove binding"));
  expect(asked).toContain("Alertmanager URL and token");
  expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  expect(badges.at(-1)).toEqual(["c1", "not-configured"]);
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
    body: {
      data: { ...BINDING, prometheusUrl: "https://saved-c1.example.com" },
    },
  };
  await click(button(root, "Save"));

  // The admin opens another cluster's binding while c1's save is in flight.
  const c2 = {
    ...BINDING,
    clusterId: "c2",
    prometheusUrl: "https://prom-c2.example.com",
    alertmanagerUrl: "",
    hasAlertmanagerToken: false,
  };
  replies.GET = { status: 200, body: { data: c2 } };
  renderForm("c2");
  await settle();
  expect(byLabel(root, "Prometheus URL").value).toBe(
    "https://prom-c2.example.com",
  );

  toasts.length = 0;
  const before = badges.length;
  release();
  await settle();

  // The late save still updates c1's badge, and only c1's.
  expect(badges.slice(before)).toEqual([["c1", "configured"]]);
  expect(byLabel(root, "Prometheus URL").value).toBe(
    "https://prom-c2.example.com",
  );
  expect(toasts).toEqual([]);
  expect(button(root, "Save").disabled).toBe(false);
});

const NOT_CONFIGURED: Reply = {
  status: 404,
  body: {
    error: {
      code: 404,
      message: "metrics are not configured",
      reason: "metrics_not_configured",
    },
  },
};

const PROM_URL = "Prometheus URL";

test("a stored binding fills both URLs but never a token, and reports configured", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();
  expect(byLabel(root, PROM_URL).value).toBe("https://prom.example.com");
  expect(byLabel(root, AM_URL).value).toBe("https://am.example.com");
  expect(byLabel(root, PROM_TOKEN).value).toBe("");
  expect(byLabel(root, AM_TOKEN).value).toBe("");
  expect(root.textContent).toContain("A token is stored for this binding.");
  expect(badges).toEqual([["c1", "configured"]]);
  expect(button(root, "Remove binding").disabled).toBe(false);
});

test("an unconfigured cluster reports not-configured without a toast and keeps Save off", async () => {
  replies.GET = NOT_CONFIGURED;
  const root = await mount();
  expect(badges).toEqual([["c1", "not-configured"]]);
  expect(toasts).toEqual([]);
  expect(byLabel(root, PROM_URL).value).toBe("");
  expect(button(root, "Save").disabled).toBe(true);
  expect(() => button(root, "Remove binding")).toThrow();
});

test("a failed load shows the server text for a 400", async () => {
  replies.GET = {
    status: 400,
    body: { error: { code: 400, message: "bad binding" } },
  };
  await mount();
  expect(toasts.at(-1)).toEqual({ message: "bad binding", type: "error" });
  expect(badges).toEqual([]);
});

test("an unexpected load failure shows the fallback, not the server text", async () => {
  replies.GET = {
    status: 500,
    body: { error: { code: 500, message: "internal detail" } },
  };
  await mount();
  expect(toasts.at(-1)).toEqual({
    message: "Could not load the binding",
    type: "error",
  });
});

test("a typed Prometheus token is sent as token and the field resets after the save", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await type(byLabel(root, PROM_TOKEN), "prom-secret");
  await click(button(root, "Save"));
  expect(puts()[0].body?.token).toBe("prom-secret");
  expect(byLabel(root, PROM_TOKEN).value).toBe("");
  expect(toasts.at(-1)).toEqual({
    message: "Metrics binding saved",
    type: "success",
  });
});

test("the Prometheus clear box sends an empty token and is disabled while one is typed", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  const clear = "Clear the stored Prometheus token on save";
  await click(byLabel(root, clear));
  await click(button(root, "Save"));
  expect(puts()[0].body?.token).toBe("");
  expect("alertmanagerToken" in (puts()[0].body as object)).toBe(false);

  await type(byLabel(root, PROM_TOKEN), "typed");
  expect(byLabel(root, clear).disabled).toBe(true);
});

test("Save stays off while the Prometheus URL is blank, and a blank Alertmanager URL is omitted", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await type(byLabel(root, AM_URL), "");
  await click(button(root, "Save"));
  expect("alertmanagerUrl" in (puts()[0].body as object)).toBe(false);

  await type(byLabel(root, PROM_URL), "  ");
  expect(button(root, "Save").disabled).toBe(true);
});

test("an unreachable Prometheus shows a fixed sentence, and other failures the fallback", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.PUT = {
    status: 502,
    body: { error: { code: 502, message: "dial tcp 10.0.0.1: refused" } },
  };
  const root = await mount();
  await click(button(root, "Save"));
  expect(toasts.at(-1)?.message).toBe("Could not reach Prometheus at that URL");

  replies.PUT = {
    status: 500,
    body: { error: { code: 500, message: "internal detail" } },
  };
  await click(button(root, "Save"));
  expect(toasts.at(-1)?.message).toBe("Failed to save the binding");
});

test("removing the binding clears the form and drops the Remove button", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.DELETE = { status: 204 };
  globalThis.confirm = () => true;
  const root = await mount();
  await click(button(root, "Remove binding"));
  expect(byLabel(root, PROM_URL).value).toBe("");
  expect(byLabel(root, AM_URL).value).toBe("");
  expect(toasts.at(-1)).toEqual({
    message: "Metrics binding removed",
    type: "success",
  });
  expect(() => button(root, "Remove binding")).toThrow();
});

test("declining the remove confirmation sends nothing", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  globalThis.confirm = () => false;
  const root = await mount();
  await click(button(root, "Remove binding"));
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  expect(byLabel(root, PROM_URL).value).toBe("https://prom.example.com");
});

test("a failed remove keeps the form and its badge as they were", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  replies.DELETE = {
    status: 500,
    body: { error: { code: 500, message: "internal detail" } },
  };
  globalThis.confirm = () => true;
  const root = await mount();
  await click(button(root, "Remove binding"));
  expect(toasts.at(-1)).toEqual({
    message: "Failed to remove the binding",
    type: "error",
  });
  expect(badges).toEqual([["c1", "configured"]]);
  expect(byLabel(root, PROM_URL).value).toBe("https://prom.example.com");
  expect(button(root, "Remove binding").disabled).toBe(false);
});

test("a remove that finishes after the form moved to another cluster badges its own cluster and leaves the new form alone", async () => {
  replies.GET = { status: 200, body: { data: BINDING } };
  globalThis.confirm = () => true;
  const root = await mount();

  let release = () => {};
  deleteGate = new Promise<void>((r) => {
    release = r;
  });
  replies.DELETE = { status: 204 };
  await click(button(root, "Remove binding"));

  replies.GET = {
    status: 200,
    body: {
      data: {
        ...BINDING,
        clusterId: "c2",
        prometheusUrl: "https://prom-c2.example.com",
      },
    },
  };
  renderForm("c2");
  await settle();

  toasts.length = 0;
  const before = badges.length;
  release();
  await settle();

  expect(badges.slice(before)).toEqual([["c1", "not-configured"]]);
  expect(byLabel(root, PROM_URL).value).toBe("https://prom-c2.example.com");
  expect(toasts).toEqual([]);
});

test("a load that answers after the form moved to another cluster leaves the new form alone", async () => {
  let releaseFirst = () => {};
  getGates = [
    new Promise<void>((r) => {
      releaseFirst = r;
    }),
  ];
  replies.GET = { status: 200, body: { data: BINDING } };
  renderForm("c1");
  await settle();

  replies.GET = {
    status: 200,
    body: {
      data: {
        ...BINDING,
        clusterId: "c2",
        prometheusUrl: "https://prom-c2.example.com",
      },
    },
  };
  renderForm("c2");
  await settle();
  const root = (view as ReturnType<typeof render>).container;
  expect(byLabel(root, PROM_URL).value).toBe("https://prom-c2.example.com");

  releaseFirst();
  await settle();
  expect(byLabel(root, PROM_URL).value).toBe("https://prom-c2.example.com");
  expect(badges).toEqual([["c2", "configured"]]);
});
