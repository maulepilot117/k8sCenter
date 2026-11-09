/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

/**
 * The ExternalSecret, ClusterExternalSecret and PushSecret lists and details:
 * search and the namespace picker narrow the rows, the force-sync button sends
 * one request and reports its result, and the drift states show their labels.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: ESOExternalSecretsList } = await import(
  "./ESOExternalSecretsList.tsx"
);
const { default: ESOExternalSecretDetail } = await import(
  "./ESOExternalSecretDetail.tsx"
);
const { default: ESOClusterExternalSecretsList } = await import(
  "./ESOClusterExternalSecretsList.tsx"
);
const { default: ESOClusterExternalSecretDetail } = await import(
  "./ESOClusterExternalSecretDetail.tsx"
);
const { default: ESOPushSecretsList } = await import(
  "./ESOPushSecretsList.tsx"
);
const { default: ESOPushSecretDetail } = await import(
  "./ESOPushSecretDetail.tsx"
);

const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify({ data }), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const requests: { url: string; method: string }[] = [];

type Route = (url: string, method: string) => Response | Promise<Response>;

function stub(route: Route) {
  requests.length = 0;
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    requests.push({ url, method });
    return route(url, method);
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  setAccessToken("t");
  selectedNamespace.value = "all";
});

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  selectedNamespace.value = "all";
});

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

const es = (name: string, namespace: string, extra = {}) => ({
  name,
  namespace,
  uid: `uid-${namespace}-${name}`,
  status: "Synced",
  storeRef: { kind: "SecretStore", name: `store-${name}` },
  targetSecretName: `target-${name}`,
  ...extra,
});

const rowNames = (container: Element) =>
  Array.from(container.querySelectorAll("a"))
    .map((a) => a.textContent)
    .filter((t) => t?.startsWith("n-"));

test("ExternalSecrets list: search and the namespace picker narrow the rows", async () => {
  stub((url) =>
    url.includes("/status")
      ? json({ detected: true })
      : json([es("n-alpha", "team-a"), es("n-beta", "team-b")]),
  );
  const { container } = render(<ESOExternalSecretsList />);
  await settle();
  expect(rowNames(container)).toEqual(["n-alpha", "n-beta"]);
  expect(container.textContent).toContain("2 of 2 ExternalSecrets");

  const search = body().getByLabelText("Search") as HTMLInputElement;
  fireEvent.input(search, { target: { value: "BETA" } });
  await settle();
  expect(rowNames(container)).toEqual(["n-beta"]);
  expect(container.textContent).toContain("1 of 2 ExternalSecrets");

  fireEvent.input(search, { target: { value: "nothing-matches" } });
  await settle();
  expect(container.textContent).toContain("No ExternalSecrets match");

  fireEvent.input(search, { target: { value: "" } });
  await settle();
  await act(async () => {
    selectedNamespace.value = "team-a";
  });
  expect(rowNames(container)).toEqual(["n-alpha"]);
  expect(container.textContent).toContain("1 of 1 ExternalSecrets (2 total)");
});

test("ExternalSecrets list: typing a namespace refetches once after the pause", async () => {
  stub((url) =>
    url.includes("/status") ? json({ detected: true }) : json([]),
  );
  render(<ESOExternalSecretsList />);
  await settle();
  const listUrls = () =>
    requests.filter((r) => r.url.includes("/externalsecrets/externalsecrets"));
  expect(listUrls()).toHaveLength(1);

  const ns = body().getByLabelText("Namespace") as HTMLInputElement;
  fireEvent.input(ns, { target: { value: "t" } });
  fireEvent.input(ns, { target: { value: "team-a" } });
  expect(listUrls()).toHaveLength(1);
  await act(async () => {
    await new Promise((r) => setTimeout(r, 400));
  });
  expect(listUrls()).toHaveLength(2);
  expect(listUrls()[1]?.url).toContain("namespace=team-a");
  expect(body().getByText("Refresh namespace")).toBeTruthy();
});

test("ExternalSecrets list: not-detected shows the install hint", async () => {
  stub(() => json({ detected: false }));
  const { container } = render(<ESOExternalSecretsList />);
  await settle();
  expect(container.textContent).toContain(
    "External Secrets Operator is not installed",
  );
});

test("ClusterExternalSecrets list: search narrows the rows", async () => {
  const ces = (name: string) => ({
    name,
    uid: `uid-${name}`,
    status: "SyncFailed",
    storeRef: { kind: "ClusterSecretStore", name: `store-${name}` },
    provisionedNamespaces: ["a", "b"],
    failedNamespaces: ["c"],
  });
  stub((url) =>
    url.includes("/status")
      ? json({ detected: true })
      : json([ces("n-one"), ces("n-two")]),
  );
  const { container } = render(<ESOClusterExternalSecretsList />);
  await settle();
  expect(rowNames(container)).toEqual(["n-one", "n-two"]);
  fireEvent.input(body().getByLabelText("Search"), {
    target: { value: "store-n-two" },
  });
  await settle();
  expect(rowNames(container)).toEqual(["n-two"]);
  expect(container.textContent).toContain("1 of 2 ClusterExternalSecrets");
});

test("PushSecrets list: search, namespace picker and typed namespace", async () => {
  const ps = (name: string, namespace: string) => ({
    name,
    namespace,
    uid: `uid-${name}`,
    status: "Synced",
    sourceSecretName: `src-${name}`,
    storeRefs: [{ kind: "SecretStore", name: "s" }],
  });
  stub((url) =>
    url.includes("/status")
      ? json({ detected: true })
      : json([ps("n-one", "team-a"), ps("n-two", "team-b")]),
  );
  const { container } = render(<ESOPushSecretsList />);
  await settle();
  expect(rowNames(container)).toEqual(["n-one", "n-two"]);
  fireEvent.input(body().getByLabelText("Search"), {
    target: { value: "src-n-one" },
  });
  await settle();
  expect(rowNames(container)).toEqual(["n-one"]);
  fireEvent.input(body().getByLabelText("Search"), { target: { value: "" } });
  await act(async () => {
    selectedNamespace.value = "team-b";
  });
  expect(rowNames(container)).toEqual(["n-two"]);

  const ns = body().getByLabelText("Namespace") as HTMLInputElement;
  fireEvent.input(ns, { target: { value: "team-b" } });
  await act(async () => {
    await new Promise((r) => setTimeout(r, 400));
  });
  expect(requests.at(-1)?.url).toContain("pushsecrets?namespace=team-b");
});

const detailRoute =
  (data: unknown, post?: Route): Route =>
  (url, method) =>
    method === "POST" && post ? post(url, method) : json(data);

test("ExternalSecret detail: drift states render their labels", async () => {
  for (const [driftStatus, reason, text] of [
    ["InSync", undefined, "matches the hash ESO recorded"],
    ["Drifted", undefined, "has been edited since ESO last reconciled"],
    ["Unknown", "secret_deleted", "has been deleted"],
  ] as const) {
    stub(
      detailRoute(
        es("db", "team-a", { driftStatus, driftUnknownReason: reason }),
      ),
    );
    const { container, unmount } = render(
      <ESOExternalSecretDetail namespace="team-a" name="db" />,
    );
    await settle();
    expect(container.textContent).toContain(driftStatus);
    expect(container.textContent).toContain(text);
    unmount();
  }
});

test("ExternalSecret detail: a double click on Force sync sends one request", async () => {
  let release: (() => void) | undefined;
  stub(
    detailRoute(es("db", "team-a"), async () => {
      await new Promise<void>((r) => {
        release = r;
      });
      return json({ status: "force-syncing" }, 202);
    }),
  );
  const { container } = render(
    <ESOExternalSecretDetail namespace="team-a" name="db" />,
  );
  await settle();
  const button = body().getByText("Force sync");
  // Both clicks land before a re-render, so only the guard can stop the second.
  await act(async () => {
    fireEvent.click(button);
    fireEvent.click(button);
  });
  await settle();
  expect(requests.filter((r) => r.method === "POST")).toHaveLength(1);
  expect(container.textContent).toContain("Force-syncing…");
  release?.();
  await settle();
  expect(container.textContent).toContain("Force-sync requested.");
  expect(body().getByText("Force sync")).toBeTruthy();
});

test("ExternalSecret detail: a refused force sync shows why", async () => {
  stub(
    detailRoute(es("db", "team-a"), () =>
      Response.json(
        { error: { code: 409, message: "busy", reason: "already_refreshing" } },
        { status: 409 },
      ),
    ),
  );
  const { container } = render(
    <ESOExternalSecretDetail namespace="team-a" name="db" />,
  );
  await settle();
  fireEvent.click(body().getByText("Force sync"));
  await settle();
  expect(container.textContent).toContain("Already refreshing");
  expect(requests.filter((r) => r.method === "POST")).toHaveLength(1);
});

test("ExternalSecret detail: an accepted force sync is observed to the outcome", async () => {
  const baseline = {
    uid: "uid-team-a-db",
    resourceVersion: "1",
    refreshTime: "2026-01-01T00:00:00Z",
    requestedAt: "2026-01-01T00:00:05Z",
  };
  let polled = false;
  stub((url, method) => {
    if (method === "POST") {
      return json(
        { status: "force-syncing", correlation: "strong", baseline },
        202,
      );
    }
    if (url.includes("/externalsecrets/team-a/db")) {
      const result = es("db", "team-a", {
        lastSyncTime: polled ? "2026-01-01T00:01:00Z" : "2026-01-01T00:00:00Z",
      });
      polled = true;
      return json(result);
    }
    return json({});
  });
  const { container } = render(
    <ESOExternalSecretDetail namespace="team-a" name="db" />,
  );
  await settle();
  fireEvent.click(body().getByText("Force sync"));
  await settle();
  expect(body().getByRole("status").textContent).toContain(
    "Waiting for External Secrets Operator",
  );
  expect((body().getByText("Force sync") as HTMLButtonElement).disabled).toBe(
    true,
  );
  await waitFor(
    () => expect(body().getByRole("status").textContent).toContain("Synced."),
    { timeout: 4000 },
  );
  expect(container.textContent).toContain("Last Sync");
  expect(requests.filter((r) => r.method === "POST")).toHaveLength(1);
}, 8000);

test("ExternalSecret detail: a load failure shows the error", async () => {
  stub(() => Response.json({ error: { code: 500 } }, { status: 500 }));
  const { container } = render(
    <ESOExternalSecretDetail namespace="team-a" name="db" />,
  );
  await settle();
  expect(container.textContent).toContain("Failed to load ExternalSecret");
});

test("ClusterExternalSecret detail: overview lists namespaces and switches tabs", async () => {
  stub(
    detailRoute({
      name: "fan",
      uid: "u1",
      status: "Synced",
      storeRef: { kind: "ClusterSecretStore", name: "css" },
      provisionedNamespaces: ["a", "b"],
      failedNamespaces: ["c"],
      namespaceSelectors: ["env=prod"],
    }),
  );
  const { container } = render(<ESOClusterExternalSecretDetail name="fan" />);
  await settle();
  expect(container.textContent).toContain("Provisioned namespaces (2)");
  expect(container.textContent).toContain("Failed namespaces (1)");
  expect(container.textContent).toContain("env=prod");
  fireEvent.click(body().getByRole("tab", { name: "Chain" }));
  expect(container.textContent).toContain("Chain visualization coming");
  expect(container.textContent).not.toContain("Provisioned namespaces");
  fireEvent.click(body().getByRole("tab", { name: "Overview" }));
  expect(container.textContent).toContain("Provisioned namespaces (2)");
});

test("PushSecret detail: overview shows push targets and switches tabs", async () => {
  stub(
    detailRoute({
      name: "pusher",
      namespace: "team-a",
      uid: "u2",
      status: "Stale",
      sourceSecretName: "src",
      storeRefs: [{ kind: "SecretStore", name: "s1" }],
    }),
  );
  const { container } = render(
    <ESOPushSecretDetail namespace="team-a" name="pusher" />,
  );
  await settle();
  expect(container.textContent).toContain("Push targets (1)");
  expect(container.textContent).toContain("SecretStore/s1");
  fireEvent.click(body().getByRole("tab", { name: "Chain" }));
  expect(container.textContent).toContain("Chain visualization coming");
  expect(container.textContent).not.toContain("Push targets");
});
