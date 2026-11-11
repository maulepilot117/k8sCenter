import { afterEach, expect, test } from "bun:test";
import type { K8sResource } from "@/lib/k8s-types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { SecretOverview } = await import(
  "@/components/k8s/detail/SecretOverview.tsx"
);
const { setAccessToken } = await import("@/lib/api.ts");

function secret(resourceVersion = "1"): K8sResource {
  return {
    apiVersion: "v1",
    kind: "Secret",
    metadata: { name: "db-creds", namespace: "db", resourceVersion },
    type: "Opaque",
    data: { password: "****", username: "****" },
  } as unknown as K8sResource;
}

const requested: string[] = [];
const pending = new Map<string, (r: Response) => void>();

afterEach(() => {
  globalThis.fetch = bunFetch;
  requested.length = 0;
  pending.clear();
  setAccessToken(null);
});

function ok(key: string, value: string): Response {
  return new Response(JSON.stringify({ data: { key, value } }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

const keyOf = (input: string | URL | Request) =>
  decodeURIComponent(String(input).split("/reveal/")[1] ?? "");

/** Every request is held until the test answers it with `answer(key, ...)`. */
function holdReveals() {
  setAccessToken("t");
  globalThis.fetch = ((input: string | URL | Request) => {
    requested.push(String(input));
    return new Promise<Response>((resolve) => {
      pending.set(keyOf(input), resolve);
    });
  }) as typeof globalThis.fetch;
}

function answer(key: string, plain: string) {
  const resolve = pending.get(key);
  if (!resolve) throw new Error(`no pending request for ${key}`);
  return act(async () => {
    resolve(ok(key, btoa(plain)));
    await new Promise((r) => setTimeout(r, 0));
  });
}

function stubReveal(values: Record<string, string>) {
  setAccessToken("t");
  globalThis.fetch = ((input: string | URL | Request) => {
    requested.push(String(input));
    const key = keyOf(input);
    return Promise.resolve(ok(key, btoa(values[key] ?? "")));
  }) as typeof globalThis.fetch;
}

const click = (label: string) =>
  act(() => {
    fireEvent.click(body().getByRole("button", { name: label }));
  });

test("values are masked until one key is revealed through the audited endpoint", async () => {
  stubReveal({ password: "hunter2" });
  render(<SecretOverview resource={secret()} />);

  expect(document.body.textContent).not.toContain("hunter2");
  await click("Reveal password");
  await waitFor(() => expect(document.body.textContent).toContain("hunter2"));

  expect(requested).toHaveLength(1);
  expect(requested[0]).toEndWith(
    "/api/v1/resources/secrets/db/db-creds/reveal/password",
  );
  // The other key stays masked.
  body().getByRole("button", { name: "Reveal username" });

  await click("Hide password");
  expect(document.body.textContent).not.toContain("hunter2");
  // Hiding is local: no second request.
  expect(requested).toHaveLength(1);
});

test("revealing key B before A resolves shows both, one request per key", async () => {
  holdReveals();
  render(<SecretOverview resource={secret()} />);

  await click("Reveal password");
  await click("Reveal username");
  expect(requested).toHaveLength(2);

  // Answer B first, then A: A's callback runs after B's state landed.
  await answer("username", "alice");
  await answer("password", "hunter2");

  await waitFor(() => {
    expect(document.body.textContent).toContain("alice");
    expect(document.body.textContent).toContain("hunter2");
  });
  expect(requested).toHaveLength(2);
  body().getByRole("button", { name: "Hide password" });
  body().getByRole("button", { name: "Hide username" });
});

test("a second click while a reveal is in flight sends no second request", async () => {
  holdReveals();
  render(<SecretOverview resource={secret()} />);

  const btn = body().getByRole("button", { name: "Reveal password" });
  act(() => {
    fireEvent.click(btn);
    fireEvent.click(btn);
  });
  expect(requested).toHaveLength(1);
  await answer("password", "hunter2");
  await waitFor(() => expect(document.body.textContent).toContain("hunter2"));
  expect(requested).toHaveLength(1);
});

test("a live update of the Secret drops revealed values", async () => {
  stubReveal({ password: "hunter2" });
  const { rerender } = render(<SecretOverview resource={secret()} />);
  await click("Reveal password");
  await waitFor(() => expect(document.body.textContent).toContain("hunter2"));

  rerender(<SecretOverview resource={secret("2")} />);
  await waitFor(() =>
    expect(document.body.textContent).not.toContain("hunter2"),
  );
});

test("a reveal still in flight when the Secret changes is discarded", async () => {
  holdReveals();
  const { rerender } = render(<SecretOverview resource={secret()} />);
  await click("Reveal password");

  rerender(<SecretOverview resource={secret("2")} />);
  await answer("password", "old-value");

  expect(document.body.textContent).not.toContain("old-value");
  await waitFor(() => body().getByRole("button", { name: "Reveal password" }));
});

test("a failed reveal shows the server message under its key and clears on retry", async () => {
  holdReveals();
  render(<SecretOverview resource={secret()} />);

  await click("Reveal password");
  await click("Reveal username");

  // username succeeds first, then password is refused with the API's error
  // envelope: the failure must not disturb the already revealed key.
  await answer("username", "alice");
  await waitFor(() => expect(document.body.textContent).toContain("alice"));
  const refuse = pending.get("password");
  if (!refuse) throw new Error("no pending request for password");
  await act(async () => {
    refuse(
      new Response(
        JSON.stringify({
          error: { code: 403, message: "not allowed to reveal this key" },
        }),
        { status: 403, headers: { "Content-Type": "application/json" } },
      ),
    );
    await new Promise((r) => setTimeout(r, 0));
  });

  await waitFor(() =>
    expect(document.body.textContent).toContain(
      "API error 403: not allowed to reveal this key",
    ),
  );
  // The other key's value stays visible; the failed key is idle again.
  expect(document.body.textContent).toContain("alice");
  body().getByRole("button", { name: "Hide username" });
  const retry = body().getByRole("button", { name: "Reveal password" });
  expect((retry as HTMLButtonElement).disabled).toBe(false);
  expect(retry.textContent).toBe("Reveal");

  // Retrying clears the stale error straight away, then shows the value.
  await click("Reveal password");
  expect(document.body.textContent).not.toContain("not allowed to reveal");
  await answer("password", "hunter2");
  await waitFor(() => expect(document.body.textContent).toContain("hunter2"));
  expect(document.body.textContent).not.toContain("not allowed to reveal");
  expect(document.body.textContent).toContain("alice");
});
