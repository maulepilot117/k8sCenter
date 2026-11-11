import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import {
  act,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

/**
 * The SecretStore and ClusterSecretStore lists narrow their rows as the
 * operator types, the namespace box refetches once after a pause, and the
 * metrics panel loads again when the store it describes changes.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: ESOStoresList } = await import("./ESOStoresList.tsx");
const { default: ESOClusterStoresList } = await import(
  "./ESOClusterStoresList.tsx"
);
const { default: ESOStoreMetricsPanel } = await import(
  "./ESOStoreMetricsPanel.tsx"
);
const { default: ESOStoreDetail } = await import("./ESOStoreDetail.tsx");
const { default: ESOClusterStoreDetail } = await import(
  "./ESOClusterStoreDetail.tsx"
);

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  selectedNamespace.value = "all";
});

function store(name: string, namespace: string | undefined, provider: string) {
  return {
    name,
    namespace,
    uid: `uid-${name}`,
    scope: namespace ? "Namespaced" : "Cluster",
    status: "Ready",
    ready: true,
    provider,
  };
}

/** Answers each request with `answer(url)` and records every URL asked. */
function stubFetch(answer: (url: string) => unknown) {
  const urls: string[] = [];
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    return json(answer(url));
  }) as unknown as typeof globalThis.fetch;
  return urls;
}

function stubFailure() {
  globalThis.fetch = (async () =>
    new Response("{}", { status: 500 })) as unknown as typeof globalThis.fetch;
}

const STORES = [
  store("vault-prod", "team-a", "vault"),
  store("aws-dev", "team-b", "aws"),
  store("vault-dev", "team-b", "vault"),
];

function listAnswer(url: string) {
  if (url.includes("/externalsecrets/status")) return { detected: true };
  if (url.includes("/externalsecrets/clusterstores")) {
    return [
      store("global-vault", undefined, "vault"),
      store("global-aws", undefined, "aws"),
    ];
  }
  if (url.includes("/externalsecrets/stores")) return STORES;
  return null;
}

test("the SecretStore search narrows the rows and the count", async () => {
  setAccessToken("t");
  stubFetch(listAnswer);
  const view = render(<ESOStoresList />);
  await settle();

  expect(view.container.textContent).toContain("3 of 3 SecretStores");
  const search = view.container.querySelector(
    "#eso-stores-search",
  ) as HTMLInputElement;
  fireEvent.input(search, { target: { value: "aws" } });

  expect(view.container.textContent).toContain("1 of 3 SecretStores");
  expect(view.container.textContent).toContain("aws-dev");
  expect(view.container.textContent).not.toContain("vault-prod");
});

test("the global namespace narrows the SecretStore rows", async () => {
  setAccessToken("t");
  stubFetch(listAnswer);
  const view = render(<ESOStoresList />);
  await settle();

  await act(async () => {
    selectedNamespace.value = "team-b";
  });

  expect(view.container.textContent).toContain("2 of 2 SecretStores");
  expect(view.container.textContent).toContain("(3 total)");
  expect(view.container.textContent).not.toContain("vault-prod");
});

test("typing in the namespace box refetches once with the typed value", async () => {
  setAccessToken("t");
  const urls = stubFetch(listAnswer);
  const view = render(<ESOStoresList />);
  await settle();
  const before = urls.filter((u) => u.includes("/externalsecrets/stores"));
  expect(before).toEqual(["/api/v1/externalsecrets/stores"]);

  const ns = view.container.querySelector("#eso-stores-ns") as HTMLInputElement;
  fireEvent.input(ns, { target: { value: "team" } });
  fireEvent.input(ns, { target: { value: "team-b" } });
  await act(async () => {
    await new Promise((r) => setTimeout(r, 450));
  });

  const after = urls.filter((u) => u.includes("/externalsecrets/stores"));
  expect(after.length).toBe(2);
  expect(after[1]).toContain("team-b");
});

test("the ClusterSecretStore search narrows the rows and the count", async () => {
  setAccessToken("t");
  stubFetch(listAnswer);
  const view = render(<ESOClusterStoresList />);
  await settle();

  expect(view.container.textContent).toContain("2 of 2 ClusterSecretStores");
  const search = view.container.querySelector(
    "#eso-cstores-search",
  ) as HTMLInputElement;
  fireEvent.input(search, { target: { value: "aws" } });

  expect(view.container.textContent).toContain("1 of 2 ClusterSecretStores");
  expect(view.container.textContent).toContain("global-aws");
  expect(view.container.textContent).not.toContain("global-vault");
});

test("the metrics panel shows the rate and loads again for another store", async () => {
  setAccessToken("t");
  const urls = stubFetch((url) => ({
    ratePerMin: url.includes("/store-b/") ? 7 : 1.5,
    last24h: 2500,
  }));
  const view = render(
    <ESOStoreMetricsPanel namespace="ns" name="store-a" scope="Namespaced" />,
  );
  await settle();
  expect(view.container.textContent).toContain("1.50");
  expect(view.container.textContent).toContain("2.5k");

  view.rerender(
    <ESOStoreMetricsPanel namespace="ns" name="store-b" scope="Namespaced" />,
  );
  await settle();

  expect(urls).toEqual([
    "/api/v1/externalsecrets/stores/ns/store-a/metrics",
    "/api/v1/externalsecrets/stores/ns/store-b/metrics",
  ]);
  expect(view.container.textContent).toContain("7.00");
});

test("the metrics panel reports offline Prometheus when the request fails", async () => {
  setAccessToken("t");
  stubFailure();
  const view = render(
    <ESOStoreMetricsPanel namespace="" name="c" scope="Cluster" />,
  );
  await settle();

  expect(view.container.textContent).toContain(
    "Rate metrics offline — Prometheus is unavailable.",
  );
});

test("the SecretStore detail loads the store and switches tabs", async () => {
  setAccessToken("t");
  const urls = stubFetch((url) => {
    if (url.endsWith("/metrics")) return { ratePerMin: null, last24h: null };
    return {
      ...store("vault-prod", "team-a", "vault"),
      providerSpec: { a: 1 },
    };
  });
  const view = render(<ESOStoreDetail namespace="team-a" name="vault-prod" />);
  await settle();

  expect(urls[0]).toBe("/api/v1/externalsecrets/stores/team-a/vault-prod");
  expect(view.container.textContent).toContain("Provider spec");
  const tabs = view.container.querySelectorAll('[role="tab"]');
  expect(tabs[0].getAttribute("aria-selected")).toBe("true");
  fireEvent.click(tabs[tabs.length - 1]);
  await waitFor(() => {
    expect(view.container.textContent).not.toContain("Provider spec");
  });
  expect(tabs[tabs.length - 1].getAttribute("aria-selected")).toBe("true");
});

test("the ClusterSecretStore detail shows the load error", async () => {
  setAccessToken("t");
  stubFailure();
  const view = render(<ESOClusterStoreDetail name="g" />);
  await settle();

  expect(view.container.textContent).toContain(
    "Failed to load ClusterSecretStore",
  );
});
