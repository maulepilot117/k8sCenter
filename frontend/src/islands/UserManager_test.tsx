import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import { toasts } from "@/src/lib/toast-store.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The local-user list loads on mount; Delete asks for the user's name first
 * and removes the row at once (restoring it if the request fails), a second
 * confirm while a request is in flight sends nothing, and Change Password
 * needs eight characters. The signed-in user cannot delete themselves.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: UserManager } = await import("./UserManager.tsx");

interface Call {
  method: string;
  url: string;
  body: string | null;
}
let calls: Call[] = [];
let release: (() => void) | null = null;
let failWrites = false;
let holdWrites = false;

const users = [
  {
    id: "u1",
    username: "alice",
    k8sUsername: "k-alice",
    k8sGroups: [],
    roles: ["admin"],
  },
  {
    id: "u2",
    username: "bob",
    k8sUsername: "k-bob",
    k8sGroups: [],
    roles: ["viewer"],
  },
];

const ok = (data: unknown) =>
  new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  calls = [];
  toasts.value = [];
  release = null;
  failWrites = false;
  holdWrites = false;
  setAccessToken("t");
  // biome-ignore lint/suspicious/noExplicitAny: a partial user is enough here
  (useAuth().user as any).value = { id: "u1", username: "alice" };
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const method = init?.method ?? "GET";
    calls.push({
      method,
      url: String(input),
      body: typeof init?.body === "string" ? init.body : null,
    });
    if (method === "GET") return ok(users);
    if (holdWrites) await new Promise<void>((r) => (release = r));
    if (failWrites) {
      return new Response(
        JSON.stringify({ error: { code: 500, message: "boom" } }),
        { status: 500, headers: { "Content-Type": "application/json" } },
      );
    }
    return ok({});
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  // biome-ignore lint/suspicious/noExplicitAny: reset the shared signal
  (useAuth().user as any).value = null;
});

const writes = () => calls.filter((c) => c.method !== "GET");

async function confirmDelete(name: string) {
  fireEvent.click(
    body().getAllByRole("button", { name: "Delete" })[name === "alice" ? 0 : 1],
  );
  fireEvent.input(body().getByPlaceholderText(name), {
    target: { value: name },
  });
  await act(async () => {
    fireEvent.click(
      body().getAllByRole("button", { name: "Delete" }).slice(-1)[0],
    );
  });
}

test("lists the users from the API and marks the signed-in one", async () => {
  render(<UserManager />);
  await settle();
  expect(calls[0].url).toBe("/api/v1/users");
  body().getByText("alice");
  body().getByText("k-bob");
  body().getByText("you");
  expect(
    (body().getAllByRole("button", { name: "Delete" })[0] as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});

test("deleting a user removes the row and sends one DELETE", async () => {
  render(<UserManager />);
  await settle();
  await confirmDelete("bob");
  await settle();
  expect(writes().map((c) => `${c.method} ${c.url}`)).toEqual([
    "DELETE /api/v1/users/u2",
  ]);
  expect(body().queryByText("k-bob")).toBeNull();
  expect(
    toasts.value.some((t) => t.message.includes('Deleted user "bob"')),
  ).toBe(true);
});

test("a failed delete puts the row back and reports the error", async () => {
  failWrites = true;
  render(<UserManager />);
  await settle();
  await confirmDelete("bob");
  await settle();
  body().getByText("k-bob");
  expect(toasts.value.some((t) => t.type === "error")).toBe(true);
});

test("a second confirm while the delete is in flight sends nothing", async () => {
  holdWrites = true;
  render(<UserManager />);
  await settle();
  fireEvent.click(body().getAllByRole("button", { name: "Delete" })[1]);
  fireEvent.input(body().getByPlaceholderText("bob"), {
    target: { value: "bob" },
  });
  const confirm = body()
    .getAllByRole("button", { name: "Delete" })
    .slice(-1)[0];
  await act(async () => {
    fireEvent.click(confirm);
    fireEvent.click(confirm);
  });
  expect(writes()).toHaveLength(1);
  await act(async () => release?.());
  await settle();
});

test("a password under eight characters cannot be saved; a valid one is PUT", async () => {
  render(<UserManager />);
  await settle();
  fireEvent.click(
    body().getAllByRole("button", { name: "Change Password" })[1],
  );
  const input = body().getByPlaceholderText("Minimum 8 characters");
  fireEvent.input(input, { target: { value: "short" } });
  body().getByText("Password must be at least 8 characters");
  const save = body().getByRole("button", {
    name: "Update Password",
  }) as HTMLButtonElement;
  expect(save.disabled).toBe(true);
  fireEvent.input(input, { target: { value: "longenough1" } });
  expect(
    (
      body().getByRole("button", {
        name: "Update Password",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Update Password" }));
  });
  await settle();
  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("PUT");
  expect(writes()[0].url).toBe("/api/v1/users/u2/password");
  expect(JSON.parse(writes()[0].body ?? "{}")).toEqual({
    password: "longenough1",
  });
  expect(body().queryByRole("dialog")).toBeNull();
});

test("Escape closes the password dialog", async () => {
  render(<UserManager />);
  await settle();
  fireEvent.click(
    body().getAllByRole("button", { name: "Change Password" })[0],
  );
  body().getByRole("dialog");
  fireEvent.keyDown(globalThis as unknown as Window, { key: "Escape" });
  expect(body().queryByRole("dialog")).toBeNull();
});

test("a failed load shows the error", async () => {
  globalThis.fetch = (async () =>
    new Response(
      JSON.stringify({ error: { code: 500, message: "no users" } }),
      {
        status: 500,
        headers: { "Content-Type": "application/json" },
      },
    )) as unknown as typeof globalThis.fetch;
  render(<UserManager />);
  await settle();
  body().getByText(/no users|Failed to load users/);
});

test("Create User opens the wizard", async () => {
  render(<UserManager />);
  await settle();
  fireEvent.click(body().getByRole("button", { name: "Create User" }));
  await settle();
  expect(body().getAllByText("Create User").length).toBeGreaterThan(1);
});
