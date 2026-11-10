/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: AlertSettings } = await import("./AlertSettings.tsx");

interface Call {
  method: string;
  url: string;
  body?: Record<string, unknown>;
}
let calls: Call[] = [];
let failPut: string | null = null;
let failTest: string | null = null;
let failGet: string | null = null;

const stored = () => ({
  enabled: true,
  webhookToken: "whk-123",
  retentionDays: 14,
  rateLimit: 60,
  smtp: {
    host: "mail.local",
    port: 25,
    username: "bob",
    password: "****",
    from: "a@b.c",
    tlsInsecure: true,
  },
});

const failure = (message: string) =>
  new Response(JSON.stringify({ error: { code: 500, message } }), {
    status: 500,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  calls = [];
  failPut = failTest = failGet = null;
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
    if (url.endsWith("/alerts/test")) {
      return failTest ? failure(failTest) : json({});
    }
    if (method === "PUT") return failPut ? failure(failPut) : json({});
    if (failGet) return failure(failGet);
    return json(stored());
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
});
afterAll(() => {
  globalThis.fetch = bunFetch;
});

function type(el: Element, value: string) {
  fireEvent.input(el, { target: { value } });
}
function inputFor(label: string): HTMLInputElement {
  return body().getByLabelText(label) as HTMLInputElement;
}
function button(name: string) {
  return body().getByRole("button", { name }) as HTMLButtonElement;
}
async function renderLoaded() {
  render(<AlertSettings />);
  await settle();
}
async function click(el: Element) {
  await act(async () => {
    fireEvent.click(el);
  });
  await settle();
}

test("the form shows the stored settings and the webhook token", async () => {
  await renderLoaded();
  expect(calls[0].method).toBe("GET");
  expect(calls[0].url).toContain("/v1/alerts/settings");
  expect(inputFor("SMTP Host").value).toBe("mail.local");
  expect(inputFor("Port").value).toBe("25");
  expect(inputFor("Username").value).toBe("bob");
  expect(inputFor("From Address").value).toBe("a@b.c");
  expect(inputFor("Rate Limit (emails/hour)").value).toBe("60");
  expect(inputFor("Retention (days)").value).toBe("14");
  const enabled = body().getByLabelText(/Enable alerting/) as HTMLInputElement;
  expect(enabled.checked).toBe(true);
  const tls = body().getByLabelText(
    /Skip TLS verification/,
  ) as HTMLInputElement;
  expect(tls.checked).toBe(true);
  expect(inputFor("Password").placeholder).toBe(
    "**** (leave empty to keep current)",
  );
  expect(inputFor("Password").value).toBe("");
  body().getByText("whk-123");
});

test("a failed load shows the error", async () => {
  failGet = "boom";
  await renderLoaded();
  body().getByText("API error 500: boom");
});

test("save sends the typed values, reports success, clears the password and reloads", async () => {
  await renderLoaded();
  type(inputFor("SMTP Host"), "smtp.example.com");
  type(inputFor("Port"), "2525");
  type(inputFor("Username"), "alice");
  type(inputFor("Password"), "hunter2");
  type(inputFor("From Address"), "x@y.z");
  type(inputFor("Rate Limit (emails/hour)"), "20");
  type(inputFor("Retention (days)"), "7");
  fireEvent.click(body().getByLabelText(/Enable alerting/));
  fireEvent.click(body().getByLabelText(/Skip TLS verification/));
  await click(button("Save Settings"));
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.body).toEqual({
    enabled: false,
    rateLimit: 20,
    retentionDays: 7,
    smtp: {
      host: "smtp.example.com",
      port: 2525,
      username: "alice",
      password: "hunter2",
      from: "x@y.z",
      tlsInsecure: false,
    },
  });
  body().getByText("Settings saved.");
  expect(calls.filter((c) => c.method === "GET")).toHaveLength(2);
  expect(inputFor("Password").value).toBe("");
  expect(inputFor("SMTP Host").value).toBe("mail.local");
});

test("unparseable numbers fall back to the defaults", async () => {
  await renderLoaded();
  type(inputFor("Port"), "");
  type(inputFor("Rate Limit (emails/hour)"), "");
  type(inputFor("Retention (days)"), "");
  await click(button("Save Settings"));
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.body).toMatchObject({
    rateLimit: 120,
    retentionDays: 30,
    smtp: { port: 587 },
  });
});

test("a failed save shows the error and re-enables the button", async () => {
  failPut = "nope";
  await renderLoaded();
  await click(button("Save Settings"));
  body().getByText("API error 500: nope");
  expect(body().queryByText("Settings saved.")).toBeNull();
  expect(button("Save Settings").disabled).toBe(false);
});

test("the test email request reports success or failure", async () => {
  await renderLoaded();
  await click(button("Send Test Email"));
  expect(
    calls.some((c) => c.method === "POST" && c.url.endsWith("/alerts/test")),
  ).toBe(true);
  body().getByText("Test email queued successfully.");

  failTest = "smtp down";
  await click(button("Send Test Email"));
  body().getByText("API error 500: smtp down");
  expect(body().queryByText("Test email queued successfully.")).toBeNull();
});

test("saving disables the save button while the request is in flight", async () => {
  await renderLoaded();
  let release: (r: Response) => void = () => {};
  const inner = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    if (init?.method === "PUT") {
      return new Promise<Response>((r) => {
        release = r;
      });
    }
    return inner(input, init);
  }) as typeof globalThis.fetch;
  await act(async () => {
    fireEvent.click(button("Save Settings"));
  });
  expect(button("Saving...").disabled).toBe(true);
  await act(async () => {
    release(json({}));
  });
  await settle();
  button("Save Settings");
});
