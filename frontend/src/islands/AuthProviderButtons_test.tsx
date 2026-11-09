/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { body, render, settle, setupDom } from "@/src/test-utils/render.tsx";

/**
 * SSO buttons appear only when the provider list has OIDC providers; local
 * and LDAP entries alone, an empty list or a failed request render nothing.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: AuthProviderButtons } = await import(
  "./AuthProviderButtons.tsx"
);

function stubFetch(responder: () => Response) {
  globalThis.fetch = (async () =>
    responder()) as unknown as typeof globalThis.fetch;
}
const data = (d: unknown, status = 200) =>
  new Response(JSON.stringify({ data: d }), {
    status,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => setAccessToken("t"));
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

test("renders a button linking to each OIDC provider", async () => {
  stubFetch(() =>
    data([
      { id: "local", type: "local", displayName: "Local" },
      {
        id: "okta",
        type: "oidc",
        displayName: "Okta",
        loginURL: "/api/v1/auth/oidc/okta/login",
      },
    ]),
  );
  const { container } = render(<AuthProviderButtons />);
  await settle();
  body().getByText("or continue with");
  body().getByRole("button", { name: "Sign in with Okta" });
  expect(container.querySelector("a")?.getAttribute("href")).toBe(
    "/api/v1/auth/oidc/okta/login",
  );
  expect(body().queryByText(/Local/)).toBeNull();
});

test("renders nothing without OIDC providers", async () => {
  stubFetch(() => data([{ id: "local", type: "local", displayName: "Local" }]));
  const { container } = render(<AuthProviderButtons />);
  await settle();
  expect(container.innerHTML).toBe("");
});

test("renders nothing when the provider request fails", async () => {
  stubFetch(() => {
    throw new TypeError("network");
  });
  const { container } = render(<AuthProviderButtons />);
  await settle();
  expect(container.innerHTML).toBe("");
});
