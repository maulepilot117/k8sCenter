/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { apiGet, setAccessToken } from "@/lib/api.ts";
import { logout } from "@/lib/auth.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * Local login posts the credentials, loads the user and sends the browser to
 * the dashboard; a rejected login stays on the page with a message for the
 * status. A 401 on any later API call refreshes the token once and retries.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: LoginForm } = await import("./LoginForm.tsx");

interface Call {
  method: string;
  url: string;
  body: string | null;
  auth: string | null;
}
let calls: Call[] = [];

type Responder = (call: Call) => Response;

function stubFetch(responder: Responder) {
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const headers = new Headers(init?.headers);
    const call: Call = {
      method: init?.method ?? "GET",
      url: String(input),
      body: typeof init?.body === "string" ? init.body : null,
      auth: headers.get("Authorization"),
    };
    calls.push(call);
    return responder(call);
  }) as unknown as typeof globalThis.fetch;
}

const data = (d: unknown, status = 200) =>
  new Response(JSON.stringify({ data: d }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
const failure = (status: number, message: string) =>
  new Response(JSON.stringify({ error: { code: status, message } }), {
    status,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  calls = [];
  setAccessToken(null);
  history.pushState({}, "", "/login");
});
// A successful login stores the user in the shared auth store, which later
// test files read; sign out so they start from an unknown user.
afterEach(async () => {
  globalThis.fetch = (async () =>
    data({})) as unknown as typeof globalThis.fetch;
  await act(async () => {
    await logout();
  });
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

async function signIn(user: string, pass: string) {
  const view = body();
  fireEvent.input(view.getByLabelText("Username"), {
    target: { value: user },
  });
  fireEvent.input(view.getByLabelText("Password"), {
    target: { value: pass },
  });
  fireEvent.submit(view.getByRole("button", { name: /sign in/i }));
  await settle();
}

test("a local login posts the credentials, loads the user and goes to the dashboard", async () => {
  stubFetch((c) =>
    c.url.endsWith("/auth/login")
      ? data({ accessToken: "tok", expiresIn: 900 })
      : data({ user: { id: "u1", username: "admin", roles: [] } }),
  );
  render(<LoginForm />);
  await signIn("admin", "hunter2");
  expect(calls[0].method).toBe("POST");
  expect(calls[0].url).toBe("/api/v1/auth/login");
  expect(JSON.parse(calls[0].body ?? "{}")).toEqual({
    username: "admin",
    password: "hunter2",
  });
  expect(calls[1].url).toContain("/auth/me");
  expect(calls[1].auth).toBe("Bearer tok");
  expect(location.pathname).toBe("/");
});

test("a 401 shows the invalid-credentials message and stays on the page", async () => {
  stubFetch(() => failure(401, "bad"));
  render(<LoginForm />);
  await signIn("admin", "wrong");
  body().getByText("Invalid username or password");
  expect(location.pathname).toBe("/login");
});

test("a 429 asks the user to wait", async () => {
  stubFetch(() => failure(429, "slow down"));
  render(<LoginForm />);
  await signIn("admin", "x");
  body().getByText("Too many attempts. Please wait a minute.");
});

test("another status shows the server's message", async () => {
  stubFetch(() => failure(500, "database down"));
  render(<LoginForm />);
  await signIn("admin", "x");
  body().getByText(/database down|Login failed/);
});

test("a network failure says the server cannot be reached", async () => {
  stubFetch(() => {
    throw new TypeError("network");
  });
  render(<LoginForm />);
  await signIn("admin", "x");
  body().getByText("Unable to connect to the server");
});

test("a 401 from an API call refreshes the token once and retries", async () => {
  setAccessToken("stale");
  stubFetch((c) => {
    if (c.url.endsWith("/auth/refresh")) return data({ accessToken: "fresh" });
    return c.auth === "Bearer fresh" ? data({ ok: true }) : failure(401, "x");
  });
  const res = await apiGet<{ ok: boolean }>("/v1/things");
  expect(res.data.ok).toBe(true);
  expect(calls.map((c) => c.url)).toEqual([
    "/api/v1/things",
    "/api/v1/auth/refresh",
    "/api/v1/things",
  ]);
});
