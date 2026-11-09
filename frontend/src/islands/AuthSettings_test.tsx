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

const { default: AuthSettings } = await import("./AuthSettings.tsx");

interface Call {
  method: string;
  url: string;
  body?: Record<string, unknown>;
}
let calls: Call[] = [];
let providers: unknown[] = [];
let failProviders = false;
let testFails: string | null = null;

beforeEach(() => {
  calls = [];
  failProviders = false;
  testFails = null;
  providers = [
    { id: "local", type: "local", displayName: "Local" },
    { id: "corp", type: "oidc", displayName: "Corp SSO" },
    { id: "dir", type: "ldap", displayName: "Directory" },
  ];
  setAccessToken("t");
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    calls.push({
      method: init?.method ?? "GET",
      url,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    });
    if (url.includes("/settings/auth/test-")) {
      return testFails
        ? new Response(
            JSON.stringify({ error: { code: 502, message: testFails } }),
            { status: 502, headers: { "Content-Type": "application/json" } },
          )
        : json({});
    }
    if (failProviders) return new Response("x", { status: 500 });
    return json(providers);
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
function input(label: string) {
  return body().getByLabelText(label) as HTMLInputElement;
}
async function renderLoaded() {
  render(<AuthSettings />);
  await settle();
}
const button = (name: string) => body().getByRole("button", { name });
async function click(el: Element) {
  await act(async () => {
    fireEvent.click(el);
  });
  await settle();
}

test("the providers tab lists each provider with its type badge", async () => {
  await renderLoaded();
  expect(calls[0].url).toContain("/v1/auth/providers");
  body().getByText("Local");
  body().getByText("Corp SSO");
  body().getByText("oidc · corp");
  body().getByText("LOCAL");
  body().getByText("OIDC", { selector: "span" });
  body().getByText("LDAP", { selector: "span" });
});

test("no providers shows the empty message", async () => {
  providers = [];
  await renderLoaded();
  body().getByText("No providers configured.");
});

test("a failed provider load fails silently and shows the empty list", async () => {
  failProviders = true;
  await renderLoaded();
  body().getByText("Configured Providers");
  body().getByText("No providers configured.");
});

test("the OIDC test needs an issuer, sends it and reports success", async () => {
  await renderLoaded();
  fireEvent.click(button("OIDC"));
  body().getByText("OIDC Provider Configuration");
  await click(button("Test Discovery"));
  expect(calls.some((c) => c.url.includes("test-oidc"))).toBe(false);

  type(input("Test Issuer URL"), "https://issuer.example");
  await click(button("Test Discovery"));
  const call = calls.find((c) => c.url.endsWith("/settings/auth/test-oidc"));
  expect(call?.method).toBe("POST");
  expect(call?.body).toEqual({ issuerURL: "https://issuer.example" });
  body().getByText("OIDC discovery successful");
});

test("the OIDC test reports a failure", async () => {
  testFails = "unreachable";
  await renderLoaded();
  fireEvent.click(button("OIDC"));
  type(input("Test Issuer URL"), "https://issuer.example");
  await click(button("Test Discovery"));
  body().getByText("OIDC test failed: API error 502: unreachable");
});

test("the LDAP test needs both fields and sends them", async () => {
  await renderLoaded();
  fireEvent.click(button("LDAP"));
  type(input("Test LDAP URL"), "ldaps://ldap.example:636");
  await click(button("Test Connection"));
  expect(calls.some((c) => c.url.includes("test-ldap"))).toBe(false);

  type(input("Test Bind DN"), "cn=ro,dc=example");
  await click(button("Test Connection"));
  const call = calls.find((c) => c.url.endsWith("/settings/auth/test-ldap"));
  expect(call?.body).toEqual({
    url: "ldaps://ldap.example:636",
    bindDN: "cn=ro,dc=example",
  });
  body().getByText("LDAP connection successful");
});

test("switching tabs clears the result and keeps the typed test values", async () => {
  await renderLoaded();
  fireEvent.click(button("OIDC"));
  type(input("Test Issuer URL"), "https://issuer.example");
  await click(button("Test Discovery"));
  body().getByText("OIDC discovery successful");
  fireEvent.click(button("LDAP"));
  expect(body().queryByText("OIDC discovery successful")).toBeNull();
  fireEvent.click(button("OIDC"));
  expect(body().queryByText("OIDC discovery successful")).toBeNull();
  expect(input("Test Issuer URL").value).toBe("https://issuer.example");
});
