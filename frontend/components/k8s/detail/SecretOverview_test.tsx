/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { SecretOverview } from "@/components/k8s/detail/SecretOverview.tsx";
import { setAccessToken } from "@/lib/api.ts";
import type { K8sResource } from "@/lib/k8s-types.ts";

const SECRET = {
  apiVersion: "v1",
  kind: "Secret",
  metadata: { name: "db-creds", namespace: "db", resourceVersion: "1" },
  type: "Opaque",
  data: { password: "****", username: "****" },
} as unknown as K8sResource;

let host: HTMLElement | null = null;
const originalFetch = globalThis.fetch;
const requested: string[] = [];

beforeAll(() => {
  if (!GlobalRegistrator.isRegistered) {
    GlobalRegistrator.register({ url: "http://localhost/config/secrets/db" });
  }
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  globalThis.fetch = originalFetch;
  requested.length = 0;
  setAccessToken(null);
});

afterAll(() => {
  if (GlobalRegistrator.isRegistered) GlobalRegistrator.unregister();
});

function stubReveal(values: Record<string, string>) {
  setAccessToken("t");
  globalThis.fetch = ((input: string | URL | Request) => {
    const url = String(input);
    requested.push(url);
    const key = decodeURIComponent(url.split("/reveal/")[1] ?? "");
    return Promise.resolve(
      new Response(JSON.stringify({ data: { key, value: values[key] } }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

const flush = () =>
  act(async () => {
    for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
  });

function button(label: string): HTMLButtonElement {
  const b = host?.querySelector(`button[aria-label="${label}"]`);
  if (!b) throw new Error(`no "${label}" button`);
  return b as HTMLButtonElement;
}

function mount(resource: K8sResource) {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(<SecretOverview resource={resource} />, host as HTMLElement),
  );
}

test("values are masked until one key is revealed through the audited endpoint", async () => {
  stubReveal({ password: btoa("hunter2") });
  mount(SECRET);

  expect(host?.textContent).not.toContain("hunter2");
  act(() => button("Reveal password").click());
  await flush();

  expect(requested).toHaveLength(1);
  expect(requested[0]).toEndWith(
    "/api/v1/resources/secrets/db/db-creds/reveal/password",
  );
  expect(host?.textContent).toContain("hunter2");
  // The other key stays masked.
  expect(() => button("Reveal username")).not.toThrow();

  act(() => button("Hide password").click());
  expect(host?.textContent).not.toContain("hunter2");
});

test("a live update of the Secret drops revealed values", async () => {
  stubReveal({ password: btoa("hunter2") });
  mount(SECRET);
  act(() => button("Reveal password").click());
  await flush();
  expect(host?.textContent).toContain("hunter2");

  const updated = {
    ...SECRET,
    metadata: { ...SECRET.metadata, resourceVersion: "2" },
  } as K8sResource;
  act(() => render(<SecretOverview resource={updated} />, host as HTMLElement));
  await flush();
  expect(host?.textContent).not.toContain("hunter2");
});
