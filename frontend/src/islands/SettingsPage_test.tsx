/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { toasts } from "@/src/lib/toast-store.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  setBrowser,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: SettingsPage } = await import("./SettingsPage.tsx");

interface Call {
  method: string;
  url: string;
  body?: Record<string, unknown>;
}
let calls: Call[] = [];
let settings: Record<string, unknown> = {};
let failGet: string | null = null;
let failPut: string | null = null;

beforeEach(() => {
  calls = [];
  failGet = failPut = null;
  toasts.value = [];
  settings = {
    monitoringPrometheusUrl: "http://p",
    monitoringGrafanaUrl: "http://g",
    monitoringGrafanaToken: "****",
    monitoringNamespace: "monitoring",
    alertingEnabled: true,
    alertingSmtpHost: "mail.local",
    alertingSmtpPort: 25,
    alertingSmtpUsername: "bob",
    alertingSmtpPassword: "****",
    alertingSmtpFrom: "a@b.c",
    alertingRateLimit: 9,
    alertingRecipients: ["x@y.z", "q@r.s"],
  };
  setAccessToken("t");
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    calls.push({
      method,
      url,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    });
    const fail = (message: string) =>
      new Response(JSON.stringify({ error: { code: 500, message } }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    if (method === "PUT") return failPut ? fail(failPut) : json({});
    return failGet ? fail(failGet) : json(settings);
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
});
afterAll(() => {
  globalThis.fetch = bunFetch;
});

function inputFor(label: string): HTMLInputElement {
  const el = body().getByText(label).parentElement?.querySelector("input");
  if (!el) throw new Error(`no input under label ${label}`);
  return el;
}
function type(el: Element, value: string) {
  fireEvent.input(el, { target: { value } });
}
const button = (name: string) =>
  body().getByRole("button", { name }) as HTMLButtonElement;
async function click(el: Element) {
  await act(async () => {
    fireEvent.click(el);
  });
  await settle();
}
async function renderLoaded() {
  render(<SettingsPage />);
  await settle();
}
function leaveIsBlocked(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(event);
  return event.defaultPrevented;
}
const messages = () => toasts.value.map((t) => `${t.type}:${t.message}`);

test("renders nothing on the server pass", () => {
  setBrowser(false);
  try {
    const { container } = render(<SettingsPage />);
    expect(container.innerHTML).toBe("");
  } finally {
    setBrowser(true);
  }
});

test("shows a loading line, then the stored values", async () => {
  render(<SettingsPage />);
  body().getByText("Loading settings…");
  await settle();
  expect(calls[0].url).toContain("/v1/settings");
  expect(inputFor("Prometheus URL").value).toBe("http://p");
  expect(inputFor("Grafana URL").value).toBe("http://g");
  expect(inputFor("Monitoring Namespace").value).toBe("monitoring");
  expect(inputFor("SMTP Host").value).toBe("mail.local");
  expect(inputFor("SMTP Port").value).toBe("25");
  expect(inputFor("Rate Limit (per hour)").value).toBe("9");
  expect(inputFor("Recipients").value).toBe("x@y.z,q@r.s");
  expect(body().getByRole("switch").getAttribute("aria-checked")).toBe("true");
  expect(button("Save Monitoring").disabled).toBe(true);
  expect(button("Save Alerting").disabled).toBe(true);
});

test("a failed load shows the error banner", async () => {
  failGet = "cannot load";
  await renderLoaded();
  body().getByText("API error 500: cannot load");
  expect(body().queryByText("Monitoring")).toBeNull();
});

test("missing stored values fall back to the defaults", async () => {
  settings = {};
  await renderLoaded();
  expect(inputFor("Prometheus URL").value).toBe("");
  expect(inputFor("SMTP Port").value).toBe("587");
  expect(inputFor("Rate Limit (per hour)").value).toBe("5");
  expect(body().getByRole("switch").getAttribute("aria-checked")).toBe("false");
});

test("editing monitoring enables only its save, which sends the typed values", async () => {
  await renderLoaded();
  type(inputFor("Prometheus URL"), "http://prom:9090");
  expect(button("Save Monitoring").disabled).toBe(false);
  expect(button("Save Alerting").disabled).toBe(true);
  expect(leaveIsBlocked()).toBe(true);

  type(inputFor("Grafana URL"), "");
  type(inputFor("Monitoring Namespace"), "obs");
  await click(button("Save Monitoring"));
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.url).toContain("/v1/settings");
  expect(put?.body).toEqual({
    monitoringPrometheusUrl: "http://prom:9090",
    monitoringGrafanaUrl: null,
    monitoringGrafanaToken: "****",
    monitoringNamespace: "obs",
  });
  expect(messages()).toEqual(["success:Monitoring settings saved"]);
  expect(button("Save Monitoring").disabled).toBe(true);
  expect(leaveIsBlocked()).toBe(false);
});

test("editing alerting sends the typed values and splits the recipients", async () => {
  await renderLoaded();
  fireEvent.click(body().getByRole("switch"));
  type(inputFor("SMTP Host"), "smtp.example.com");
  type(inputFor("SMTP Port"), "2525");
  type(inputFor("SMTP Username"), "alice");
  type(inputFor("SMTP Password"), "hunter2");
  type(inputFor("From Address"), "f@y.z");
  type(inputFor("Rate Limit (per hour)"), "20");
  type(inputFor("Recipients"), " a@b.c , ,d@e.f ");
  expect(button("Save Monitoring").disabled).toBe(true);
  await click(button("Save Alerting"));
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.body).toEqual({
    alertingEnabled: false,
    alertingSmtpHost: "smtp.example.com",
    alertingSmtpPort: 2525,
    alertingSmtpUsername: "alice",
    alertingSmtpPassword: "hunter2",
    alertingSmtpFrom: "f@y.z",
    alertingRateLimit: 20,
    alertingRecipients: ["a@b.c", "d@e.f"],
  });
  expect(messages()).toEqual(["success:Alerting settings saved"]);
  expect(button("Save Alerting").disabled).toBe(true);
});

test("an empty recipients list is sent as null", async () => {
  await renderLoaded();
  type(inputFor("Recipients"), "");
  await click(button("Save Alerting"));
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.body?.alertingRecipients).toBeNull();
});

test("a failed save toasts the error and keeps the section dirty", async () => {
  failPut = "denied";
  await renderLoaded();
  type(inputFor("Prometheus URL"), "http://x");
  await click(button("Save Monitoring"));
  expect(messages()).toEqual(["error:API error 500: denied"]);
  expect(button("Save Monitoring").disabled).toBe(false);
  expect(leaveIsBlocked()).toBe(true);
});

test("the section being saved shows its own saving state", async () => {
  await renderLoaded();
  type(inputFor("Prometheus URL"), "http://x");
  type(inputFor("SMTP Host"), "h");
  let release: (r: Response) => void = () => {};
  globalThis.fetch = (() =>
    new Promise<Response>((r) => {
      release = r;
    })) as unknown as typeof globalThis.fetch;
  await act(async () => {
    fireEvent.click(button("Save Monitoring"));
  });
  expect(button("Save Monitoring").disabled).toBe(true);
  expect(button("Save Alerting").disabled).toBe(false);
  await act(async () => {
    release(json({}));
  });
  await settle();
  expect(button("Save Alerting").disabled).toBe(false);
});

test("Send Test Email puts to the alerts test endpoint and toasts", async () => {
  await renderLoaded();
  await click(button("Send Test Email"));
  const put = calls.find((c) => c.url.endsWith("/v1/alerts/test"));
  expect(put?.method).toBe("PUT");
  expect(messages()).toEqual(["success:Test email sent"]);

  failPut = "smtp down";
  toasts.value = [];
  await click(button("Send Test Email"));
  expect(messages()).toEqual(["error:API error 500: smtp down"]);
});
