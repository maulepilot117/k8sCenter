/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { logout } from "@/lib/auth.ts";
import {
  act,
  body,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The OIDC callback page trades the httpOnly cookie for an access token, then
 * goes to the dashboard; a failed exchange offers a way back to the login.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: OIDCCallbackHandler } = await import(
  "./OIDCCallbackHandler.tsx"
);

let urls: string[] = [];
function stubFetch(responder: (url: string) => Response) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    urls.push(String(input));
    return responder(String(input));
  }) as unknown as typeof globalThis.fetch;
}
const data = (d: unknown, status = 200) =>
  new Response(JSON.stringify({ data: d }), {
    status,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  urls = [];
  setAccessToken(null);
  history.pushState({}, "", "/auth/callback");
});
// The exchange stores the user in the shared auth store, which later test
// files read; sign out so they start from an unknown user.
afterEach(async () => {
  globalThis.fetch = (async () =>
    data({})) as unknown as typeof globalThis.fetch;
  await act(async () => {
    await logout();
  });
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

test("shows progress while the exchange runs, then goes to the dashboard", async () => {
  stubFetch((url) =>
    url.endsWith("/auth/oidc-token-exchange")
      ? data({ accessToken: "tok" })
      : data({ user: { id: "u1", username: "a", roles: [] } }),
  );
  render(<OIDCCallbackHandler />);
  body().getByText("Completing sign in...");
  await settle();
  expect(urls[0]).toContain("/auth/oidc-token-exchange");
  expect(location.pathname).toBe("/");
});

test("a failed exchange shows the error and a link back to the login", async () => {
  stubFetch(() => data({ accessToken: "" }));
  render(<OIDCCallbackHandler />);
  await settle();
  body().getByText("Authentication failed. Please try again.");
  expect(
    body().getByRole("link", { name: "Back to login" }).getAttribute("href"),
  ).toBe("/login");
  expect(location.pathname).toBe("/auth/callback");
});
