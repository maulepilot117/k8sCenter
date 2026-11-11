import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";
import { toasts } from "@/src/lib/toast-store.ts";
import {
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The Application and ApplicationSet detail pages: tabs switch, the sync,
 * suspend, rollback, refresh and delete actions each send one request after
 * their confirmation, and the result shows as a toast.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: GitOpsAppDetail } = await import("./GitOpsAppDetail.tsx");
const { default: GitOpsAppSetDetail } = await import(
  "./GitOpsAppSetDetail.tsx"
);

interface Sent {
  method: string;
  url: string;
  body: string | null;
}
const sent: Sent[] = [];

beforeEach(() => {
  // A remote selection keeps the live-update socket closed.
  switchCluster("remote-1", "gen-1");
  setAccessToken("t");
  sent.length = 0;
  toasts.value = [];
});

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  toasts.value = [];
  switchCluster(LOCAL_CLUSTER_ID, "local");
});

const APP = {
  id: "argo:argocd:guestbook",
  name: "guestbook",
  namespace: "argocd",
  tool: "argocd",
  kind: "Application",
  syncStatus: "outofsync",
  healthStatus: "healthy",
  source: { repoURL: "ssh://git/guestbook.git", path: "deploy" },
  managedResourceCount: 1,
  suspended: false,
};

const APP_DETAIL = {
  app: APP,
  resources: [
    {
      kind: "Deployment",
      namespace: "web",
      name: "guestbook-ui",
      status: "Synced",
    },
  ],
  history: [
    {
      revision: "aaaaaaa1111",
      status: "Synced",
      deployedAt: "2026-10-01T00:00:00Z",
    },
    {
      revision: "bbbbbbb2222",
      status: "Synced",
      deployedAt: "2026-10-02T00:00:00Z",
    },
  ],
};

const APPSET_DETAIL = {
  appSet: {
    id: "argocd:argocd:team-apps",
    name: "team-apps",
    namespace: "argocd",
    tool: "argocd",
    generatorTypes: ["list"],
    templateSource: { repoURL: "https://example.test/apps.git" },
    templateDestination: "in-cluster",
    status: "Healthy",
    generatedAppCount: 2,
    summary: {
      total: 0,
      synced: 0,
      outOfSync: 0,
      degraded: 0,
      progressing: 0,
      suspended: 0,
    },
    preserveOnDeletion: false,
    createdAt: "2026-10-01T00:00:00Z",
  },
  generators: [{ list: { elements: [{ cluster: "prod-east" }] } }],
  conditions: [],
  applications: [],
};

/** Answers GETs with `detail`, and writes with a message. */
function serve(detail: unknown) {
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const req = input instanceof Request ? input : null;
    const url = String(req ? req.url : input);
    const method = (req?.method ?? init?.method ?? "GET").toUpperCase();
    const text = req
      ? await req.clone().text()
      : (init?.body as string | undefined);
    sent.push({ method, url, body: text ? String(text) : null });
    const data = method === "GET" ? detail : { message: "Done." };
    return new Response(JSON.stringify({ data }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
}

const writes = () => sent.filter((s) => s.method !== "GET");
const loads = () =>
  sent.filter((s) => s.method === "GET" && !s.url.includes("/commits"));

/** The dialog renders after the page, so its button is the last with this name. */
function dialogButton(label: string): HTMLButtonElement {
  return body()
    .getAllByRole("button", { name: label })
    .at(-1) as HTMLButtonElement;
}

/** Clicks the dialog's button with this label. */
function confirmWith(label: string) {
  fireEvent.click(dialogButton(label));
}

test("Sync asks for confirmation, then sends one request, shows the result and reloads", async () => {
  serve(APP_DETAIL);
  render(<GitOpsAppDetail id={APP.id} />);
  await settle();
  expect(loads()).toHaveLength(1);

  fireEvent.click(body().getByRole("button", { name: "Sync" }));
  await settle();
  expect(writes()).toHaveLength(0);
  expect(body().getByText("Sync guestbook?")).toBeTruthy();

  confirmWith("Sync");
  await settle();

  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("POST");
  expect(writes()[0].url).toContain(
    `/v1/gitops/applications/${encodeURIComponent(APP.id)}/sync`,
  );
  expect(toasts.value.map((t) => t.message)).toEqual(["Done."]);
  expect(loads()).toHaveLength(2);
  expect(body().queryByRole("dialog")).toBeNull();
});

test("Resume sends suspend false without asking", async () => {
  serve({ ...APP_DETAIL, app: { ...APP, suspended: true } });
  render(<GitOpsAppDetail id={APP.id} />);
  await settle();

  fireEvent.click(body().getByRole("button", { name: "Resume" }));
  await settle();

  expect(writes()).toHaveLength(1);
  expect(writes()[0].body).toBe(JSON.stringify({ suspend: false }));
});

test("Suspend asks first, then sends suspend true", async () => {
  serve(APP_DETAIL);
  render(<GitOpsAppDetail id={APP.id} />);
  await settle();

  fireEvent.click(body().getByRole("button", { name: "Suspend" }));
  await settle();
  expect(writes()).toHaveLength(0);

  confirmWith("Suspend");
  await settle();

  expect(writes()).toHaveLength(1);
  expect(writes()[0].body).toBe(JSON.stringify({ suspend: true }));
});

test("the History tab rolls back to the chosen revision after confirming", async () => {
  serve(APP_DETAIL);
  render(<GitOpsAppDetail id={APP.id} />);
  await settle();

  fireEvent.click(body().getByRole("tab", { name: "History" }));
  await settle();
  expect(body().getByText("bbbbbbb")).toBeTruthy();

  fireEvent.click(body().getAllByRole("button", { name: "Rollback" })[1]);
  await settle();
  expect(writes()).toHaveLength(0);

  confirmWith("Rollback");
  await settle();

  expect(writes()).toHaveLength(1);
  expect(writes()[0].url).toContain("/rollback");
  expect(writes()[0].body).toBe(JSON.stringify({ revision: "bbbbbbb2222" }));
});

test("the Resources tab lists the managed resources", async () => {
  serve(APP_DETAIL);
  render(<GitOpsAppDetail id={APP.id} />);
  await settle();
  expect(body().queryByText("guestbook-ui")).toBeNull();

  fireEvent.click(body().getByRole("tab", { name: /Resources/ }));
  await settle();

  expect(body().getByText("guestbook-ui")).toBeTruthy();
});

test("a failed Application load shows an error and Retry reloads it", async () => {
  globalThis.fetch = (async () =>
    new Response("{}", { status: 500 })) as unknown as typeof globalThis.fetch;
  render(<GitOpsAppDetail id={APP.id} />);
  await settle();
  expect(body().getByText("Failed to load application")).toBeTruthy();

  serve(APP_DETAIL);
  fireEvent.click(body().getByRole("button", { name: "Retry" }));
  await settle();

  expect(body().getByRole("button", { name: "Sync" })).toBeTruthy();
});

test("ApplicationSet generators expand and collapse on click", async () => {
  serve(APPSET_DETAIL);
  render(<GitOpsAppSetDetail id="argocd:argocd:team-apps" />);
  await settle();

  expect(document.body.textContent).not.toContain("prod-east");
  fireEvent.click(body().getByText("Expand"));
  await settle();
  expect(document.body.textContent).toContain("prod-east");

  fireEvent.click(body().getByText("Collapse"));
  await settle();
  expect(document.body.textContent).not.toContain("prod-east");
});

test("the ApplicationSet Template tab shows the template source", async () => {
  serve(APPSET_DETAIL);
  render(<GitOpsAppSetDetail id="argocd:argocd:team-apps" />);
  await settle();

  fireEvent.click(body().getByRole("tab", { name: "Template" }));
  await settle();

  expect(body().getByText("https://example.test/apps.git")).toBeTruthy();
});

test("ApplicationSet Refresh sends one request, shows the result and reloads", async () => {
  serve(APPSET_DETAIL);
  render(<GitOpsAppSetDetail id="argocd:argocd:team-apps" />);
  await settle();
  expect(loads()).toHaveLength(1);

  fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  await settle();

  expect(writes()).toHaveLength(1);
  expect(writes()[0].url).toContain("/refresh");
  expect(toasts.value.map((t) => t.message)).toEqual(["Done."]);
  expect(loads()).toHaveLength(2);
});

test("ApplicationSet Delete needs the name typed before it can be confirmed", async () => {
  serve(APPSET_DETAIL);
  render(<GitOpsAppSetDetail id="argocd:argocd:team-apps" />);
  await settle();

  fireEvent.click(body().getByRole("button", { name: "Delete" }));
  await settle();
  const dialog = body().getByRole("dialog");
  const confirm = dialogButton("Delete");
  expect(confirm.disabled).toBe(true);

  fireEvent.input(dialog.querySelector("input") as HTMLInputElement, {
    target: { value: "team-apps" },
  });
  await settle();
  expect(confirm.disabled).toBe(false);
  expect(writes()).toHaveLength(0);
});
