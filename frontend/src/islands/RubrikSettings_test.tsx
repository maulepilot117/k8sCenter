import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import type { UserInfo } from "@/lib/k8s-types.ts";
import type {
  RubrikConnection,
  RubrikMapping,
  RubrikStatus,
} from "@/lib/rubrik-types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * Rubrik settings page: who sees the form, the provider states, the
 * connection save (secret handling, a new origin, mappings cleared) and the
 * cluster mappings (invalid, local id, RSC failure). fetch is routed by
 * method and URL; each test sets the replies it needs.
 */
setupDom({ browser: true });

const toasts: Array<{ message: string; type: string }> = [];
// bun shares modules across test files, so the real module is kept and put
// back in afterAll.
const realToastStore = { ...(await import("@/src/lib/toast-store.ts")) };
mock.module("@/src/lib/toast-store.ts", () => ({
  ...realToastStore,
  showToast: (message: string, type = "info") => {
    toasts.push({ message, type });
  },
}));

const { default: RubrikSettings } = await import("./RubrikSettings.tsx");
const { useAuth } = await import("@/lib/auth.ts");
const { reloadRubrikStatus, resetRubrikStatusForTest } = await import(
  "@/src/lib/rubrik-status.ts"
);

const bunFetch = globalThis.fetch;
const realConfirm = globalThis.confirm;

afterAll(() => {
  mock.module("@/src/lib/toast-store.ts", () => realToastStore);
  globalThis.fetch = bunFetch;
  globalThis.confirm = realConfirm;
});

const SECRET = "s3cr3t-never-shown";
const RSC_A = "11111111-1111-4111-8111-111111111111";
const RSC_B = "22222222-2222-4222-8222-222222222222";

function user(roles: string[]): UserInfo {
  return {
    id: "u1",
    username: roles.includes("admin") ? "alice" : "bob",
    provider: "local",
    kubernetesUsername: "alice",
    kubernetesGroups: [],
    roles,
  };
}

function connection(over: Partial<RubrikConnection> = {}): RubrikConnection {
  return {
    configured: true,
    tenantUrl: "https://acme.my.rubrik.com",
    clientId: "client-1",
    hasSecret: true,
    revision: 3,
    health: "ok",
    lastSuccessAt: new Date(Date.now() - 5 * 60_000).toISOString(),
    ...over,
  };
}

function status(conn: RubrikConnection | undefined): RubrikStatus {
  return {
    enabled: true,
    preview: true,
    writesEnabled: false,
    cluster: { id: "local", state: "mapped" },
    connection: conn,
  };
}

function mapping(over: Partial<RubrikMapping> = {}): RubrikMapping {
  return {
    clusterId: "local",
    rscClusterId: RSC_A,
    rscClusterName: "prod-east",
    state: "mapped",
    updatedBy: "alice",
    updatedAt: "2026-11-20T00:00:00Z",
    ...over,
  };
}

const reply = (code: number, payload?: unknown): Response =>
  new Response(payload === undefined ? null : JSON.stringify(payload), {
    status: code,
    headers: { "Content-Type": "application/json" },
  });
const ok = (data: unknown) => reply(200, { data });
const fail = (code: number, reason: string | undefined, message: string) =>
  reply(code, { error: { code, message, reason } });

interface Call {
  method: string;
  url: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];
let routes: Record<string, () => Response | Promise<Response>> = {};

function install() {
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    calls.push({
      method,
      url,
      body: init?.body ? JSON.parse(String(init.body)) : null,
    });
    const route = routes[`${method} ${url}`];
    if (route) return route();
    return fail(599, "unstubbed", `${method} ${url}`);
  }) as unknown as typeof globalThis.fetch;
}

/** The replies for an admin with a working connection and one local mapping. */
function defaults(conn = connection(), mappings = [mapping()]) {
  routes = {
    "GET /api/v1/rubrik/status": () => ok(status(conn)),
    "GET /api/v1/clusters": () =>
      ok([
        { id: "home", name: "home", displayName: "Home", isLocal: true },
        { id: "c-2", name: "edge", displayName: "", isLocal: false },
      ]),
    "GET /api/v1/rubrik/mappings": () => ok(mappings),
    "GET /api/v1/rubrik/rsc-clusters": () =>
      ok([
        {
          family: "namespace",
          id: RSC_A,
          name: "prod-east",
          mappedTo: "local",
        },
        { family: "namespace", id: RSC_B, name: "prod-west" },
      ]),
  };
}

beforeEach(() => {
  calls = [];
  toasts.length = 0;
  resetRubrikStatusForTest();
  setAccessToken("t");
  useAuth().user.value = user(["admin"]);
  globalThis.confirm = () => true;
  defaults();
  install();
});

afterEach(() => {
  useAuth().user.value = null;
  resetRubrikStatusForTest();
});

const tenantInput = () =>
  document.getElementById("rubrik-tenant-url") as HTMLInputElement | null;
const secretInput = () =>
  document.getElementById("rubrik-client-secret") as HTMLInputElement;

test("a non-admin sees that the page is for admins and no form", async () => {
  useAuth().user.value = user(["viewer"]);
  const { unmount } = render(<RubrikSettings />);
  await settle();
  body().getByText(/Only admins can see and change/);
  expect(tenantInput()).toBeNull();
  expect(calls.some((c) => c.url.includes("/rubrik/mappings"))).toBe(false);
  expect(calls.some((c) => c.url.includes("/rubrik/rsc-clusters"))).toBe(false);
  unmount();
});

test("a 404 status says the provider is not enabled", async () => {
  routes["GET /api/v1/rubrik/status"] = () =>
    fail(404, undefined, "404 page not found");
  const { unmount } = render(<RubrikSettings />);
  await settle();
  body().getByText(/The Rubrik provider is not enabled/);
  expect(tenantInput()).toBeNull();
  unmount();
});

test("a failed status read says so and retries", async () => {
  routes["GET /api/v1/rubrik/status"] = () => fail(500, undefined, "boom");
  const { unmount } = render(<RubrikSettings />);
  await settle();
  body().getByText("The Rubrik status could not be loaded.");
  expect(body().queryByText(/not enabled/)).toBeNull();
  routes["GET /api/v1/rubrik/status"] = () => ok(status(connection()));
  fireEvent.click(body().getByRole("button", { name: "Retry" }));
  await settle();
  expect(tenantInput()?.value).toBe("https://acme.my.rubrik.com");
  unmount();
});

test("the form shows health and that a secret is set, never the secret", async () => {
  const { unmount } = render(<RubrikSettings />);
  await settle();
  expect(body().getByTestId("rubrik-health").textContent).toContain(
    "Connected",
  );
  body().getByText("Secret set.");
  expect(secretInput().type).toBe("password");
  expect(secretInput().autocomplete).toBe("new-password");
  expect(secretInput().value).toBe("");
  body().getByText("Preview");
  unmount();
});

test("saving on a new origin without the secret shows the API's re-entry error", async () => {
  routes["PUT /api/v1/rubrik/connection"] = () =>
    fail(
      400,
      "secret_required",
      "enter the client secret: none is stored for this tenant address",
    );
  const { unmount } = render(<RubrikSettings />);
  await settle();
  fireEvent.change(tenantInput() as HTMLInputElement, {
    target: { value: "https://other.my.rubrik.com" },
  });
  body().getByText(/This is a different tenant\. Enter its client secret/);
  body().getByText(/Saving also clears every cluster mapping/);
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Save connection" }));
  });
  await settle();
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.body).toEqual({
    tenantUrl: "https://other.my.rubrik.com",
    clientId: "client-1",
  });
  expect(body().getByRole("alert").textContent).toContain(
    "enter the client secret: none is stored for this tenant address",
  );
  unmount();
});

test("the secret is sent once and never appears in the page after the save", async () => {
  const moved = connection({
    tenantUrl: "https://other.my.rubrik.com",
    revision: 4,
    health: "unknown",
    lastSuccessAt: undefined,
  });
  routes["PUT /api/v1/rubrik/connection"] = () =>
    ok({ ...moved, mappingsCleared: true });
  const view = render(<RubrikSettings />);
  await settle();
  fireEvent.change(tenantInput() as HTMLInputElement, {
    target: { value: "https://other.my.rubrik.com" },
  });
  fireEvent.change(secretInput(), { target: { value: SECRET } });
  // The status read after the save answers with the new revision.
  routes["GET /api/v1/rubrik/status"] = () => ok(status(moved));
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Save connection" }));
  });
  await settle();
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.body?.clientSecret).toBe(SECRET);
  expect(secretInput().value).toBe("");
  expect(view.container.innerHTML).not.toContain(SECRET);
  expect(document.body.innerHTML).not.toContain(SECRET);
  expect(toasts.map((t) => t.message)).toEqual([
    "Rubrik connection saved",
    "The tenant changed, so every cluster mapping was cleared. Map the clusters again.",
  ]);
  expect(tenantInput()?.value).toBe("https://other.my.rubrik.com");
  view.unmount();
});

test("an invalid mapping asks for the mapping to be confirmed again", async () => {
  defaults(connection(), [mapping({ state: "mapping_invalid" })]);
  routes["PUT /api/v1/rubrik/mappings/local"] = () =>
    ok(mapping({ state: "mapped" }));
  const { unmount } = render(<RubrikSettings />);
  await settle();
  const row = body().getByTestId("rubrik-mapping-local");
  expect(row.textContent).toContain(
    "This mapping no longer matches the cluster's registration",
  );
  expect(row.textContent).toContain("Was mapped to prod-east");
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Confirm mapping" }));
  });
  await settle();
  const put = calls.find(
    (c) => c.method === "PUT" && c.url === "/api/v1/rubrik/mappings/local",
  );
  expect(put?.body).toEqual({ rscClusterId: RSC_A, connectionRevision: 3 });
  unmount();
});

test("the local cluster maps under the id local and a taken RSC cluster is disabled", async () => {
  defaults(connection(), []);
  routes["GET /api/v1/rubrik/rsc-clusters"] = () =>
    ok([
      { family: "namespace", id: RSC_A, name: "prod-east", mappedTo: "c-2" },
      { family: "protectionSet", id: RSC_B, name: "prod-west" },
    ]);
  routes["PUT /api/v1/rubrik/mappings/local"] = () =>
    ok(mapping({ rscClusterId: RSC_B, rscClusterName: "prod-west" }));
  const { unmount } = render(<RubrikSettings />);
  await settle();
  const local = body().getByTestId("rubrik-mapping-local");
  expect(local.textContent).toContain("Home (local)");
  expect(local.textContent).toContain("Not mapped");
  const select = body().getByLabelText(
    "RSC cluster for Home (local)",
  ) as HTMLSelectElement;
  const taken = Array.from(select.options).find((o) => o.value === RSC_A);
  expect(taken?.disabled).toBe(true);
  expect(taken?.textContent).toContain("mapped to edge");
  expect(taken?.textContent).not.toContain("c-2");
  fireEvent.change(select, { target: { value: RSC_B } });
  await act(async () => {
    fireEvent.click(
      body().getAllByRole("button", { name: "Save mapping" })[0] as HTMLElement,
    );
  });
  await settle();
  expect(
    calls.some(
      (c) => c.method === "PUT" && c.url === "/api/v1/rubrik/mappings/local",
    ),
  ).toBe(true);
  expect(toasts.map((t) => t.message)).toContain("Home (local) mapped");
  unmount();
});

test("a failed RSC cluster list shows RSC's fixed message and no picker", async () => {
  routes["GET /api/v1/rubrik/rsc-clusters"] = () =>
    fail(502, "unreachable", "RSC could not be reached");
  const { unmount } = render(<RubrikSettings />);
  await settle();
  body().getByText(
    "RSC's clusters could not be listed: RSC could not be reached",
  );
  expect(body().queryByLabelText("RSC cluster for Home (local)")).toBeNull();
  expect(body().getByTestId("rubrik-mapping-local").textContent).toContain(
    "Mapped to prod-east",
  );
  unmount();
});

test("removing a mapping asks first and sends the delete", async () => {
  routes["DELETE /api/v1/rubrik/mappings/local"] = () => reply(204);
  let asked = "";
  globalThis.confirm = (msg?: string) => {
    asked = String(msg);
    return true;
  };
  const { unmount } = render(<RubrikSettings />);
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Remove" }));
  });
  await settle();
  expect(asked).toBe("Remove the Rubrik mapping for Home (local)?");
  expect(
    calls.some(
      (c) => c.method === "DELETE" && c.url === "/api/v1/rubrik/mappings/local",
    ),
  ).toBe(true);
  unmount();
});

test("without a stored secret the mappings wait for the connection", async () => {
  defaults(connection({ hasSecret: false, health: "secret_required" }));
  const { unmount } = render(<RubrikSettings />);
  await settle();
  body().getByText(/Save the connection first/);
  body().getByText("No secret is stored.");
  expect(calls.some((c) => c.url.includes("/rubrik/rsc-clusters"))).toBe(false);
  unmount();
});

const CONFLICT = /changed while you were editing, so nothing was saved/;

test("a connection save refused as changed keeps the form and says why after the reload", async () => {
  routes["PUT /api/v1/rubrik/connection"] = () =>
    fail(409, "connection_changed", "the connection changed; reload it");
  const { unmount } = render(<RubrikSettings />);
  await settle();
  // The status reload hangs until released, so the page is seen mid-reload.
  let release: (r: Response) => void = () => {};
  routes["GET /api/v1/rubrik/status"] = () =>
    new Promise<Response>((resolve) => {
      release = resolve;
    });
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Save connection" }));
  });
  await settle();
  expect(body().queryByText("Loading Rubrik status…")).toBeNull();
  expect(tenantInput()).not.toBeNull();
  expect(body().getByRole("alert").textContent).toMatch(CONFLICT);
  await act(async () => {
    release(ok(status(connection({ revision: 4, clientId: "client-2" }))));
  });
  await settle();
  // The new revision remounts the form; the notice is held by the page.
  expect(
    (document.getElementById("rubrik-client-id") as HTMLInputElement).value,
  ).toBe("client-2");
  expect(body().getByRole("alert").textContent).toMatch(CONFLICT);
  unmount();
});

test("a mapping save refused as changed says why, and the next save clears it", async () => {
  defaults(connection(), []);
  routes["PUT /api/v1/rubrik/mappings/local"] = () =>
    fail(409, "connection_changed", "the connection changed; reload it");
  const { unmount } = render(<RubrikSettings />);
  await settle();
  routes["GET /api/v1/rubrik/status"] = () =>
    ok(status(connection({ revision: 4 })));
  const pick = () =>
    body().getByLabelText("RSC cluster for Home (local)") as HTMLSelectElement;
  fireEvent.change(pick(), { target: { value: RSC_B } });
  await act(async () => {
    fireEvent.click(
      body().getAllByRole("button", { name: "Save mapping" })[0] as HTMLElement,
    );
  });
  await settle();
  const alerts = body().getAllByRole("alert");
  expect(alerts).toHaveLength(1);
  expect(alerts[0]?.textContent).toMatch(CONFLICT);
  // The save after the reload carries the new revision and clears the notice.
  routes["PUT /api/v1/rubrik/mappings/local"] = () =>
    ok(mapping({ rscClusterId: RSC_B, rscClusterName: "prod-west" }));
  fireEvent.change(pick(), { target: { value: RSC_B } });
  await act(async () => {
    fireEvent.click(
      body().getAllByRole("button", { name: "Save mapping" })[0] as HTMLElement,
    );
  });
  await settle();
  const puts = calls.filter(
    (c) => c.method === "PUT" && c.url === "/api/v1/rubrik/mappings/local",
  );
  expect(puts.at(-1)?.body).toEqual({
    rscClusterId: RSC_B,
    connectionRevision: 4,
  });
  expect(body().queryByText(CONFLICT)).toBeNull();
  unmount();
});

const HELM_TENANT = "https://helm.my.rubrik.com";

/** No connection saved; Helm's rubrik.tenantUrl arrives as the suggestion. */
function unsaved(over: Partial<RubrikConnection> = {}): RubrikConnection {
  return {
    configured: false,
    hasSecret: false,
    health: "not_configured",
    suggestedTenantUrl: HELM_TENANT,
    ...over,
  };
}

const tenantNote = () => body().queryByText(/Filled in from the Helm value/);

test("an unsaved connection pre-fills Helm's tenant URL and says where it came from", async () => {
  defaults(unsaved());
  routes["PUT /api/v1/rubrik/connection"] = () =>
    fail(400, "invalid_connection", "nope");
  const { unmount } = render(<RubrikSettings />);
  await settle();
  const input = tenantInput() as HTMLInputElement;
  expect(input.value).toBe(HELM_TENANT);
  expect(tenantNote()).not.toBeNull();
  expect(input.getAttribute("aria-describedby")).toBe("rubrik-tenant-url-note");
  // Nothing is saved on load: only the status was read.
  expect(calls.some((c) => c.method !== "GET")).toBe(false);
  fireEvent.change(document.getElementById("rubrik-client-id") as HTMLElement, {
    target: { value: "client-9" },
  });
  fireEvent.change(secretInput(), { target: { value: SECRET } });
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Save connection" }));
  });
  await settle();
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.body).toEqual({
    tenantUrl: HELM_TENANT,
    clientId: "client-9",
    clientSecret: SECRET,
  });
  unmount();
});

test("editing the pre-filled URL drops the Helm note", async () => {
  defaults(unsaved());
  const { unmount } = render(<RubrikSettings />);
  await settle();
  fireEvent.change(tenantInput() as HTMLInputElement, {
    target: { value: "https://typed.my.rubrik.com" },
  });
  expect(tenantNote()).toBeNull();
  expect(tenantInput()?.getAttribute("aria-describedby")).toBeNull();
  unmount();
});

test("a saved connection wins over a Helm suggestion", async () => {
  defaults(connection({ suggestedTenantUrl: HELM_TENANT }));
  const { unmount } = render(<RubrikSettings />);
  await settle();
  expect(tenantInput()?.value).toBe("https://acme.my.rubrik.com");
  expect(tenantNote()).toBeNull();
  unmount();
});

test("without a Helm suggestion an unsaved connection starts empty", async () => {
  defaults(unsaved({ suggestedTenantUrl: undefined }));
  const { unmount } = render(<RubrikSettings />);
  await settle();
  expect(tenantInput()?.value).toBe("");
  expect(tenantNote()).toBeNull();
  unmount();
});

test("a saved URL equal to the Helm value is not labelled as from Helm", async () => {
  defaults(connection({ suggestedTenantUrl: "https://acme.my.rubrik.com" }));
  const { unmount } = render(<RubrikSettings />);
  await settle();
  expect(tenantInput()?.value).toBe("https://acme.my.rubrik.com");
  expect(tenantNote()).toBeNull();
  unmount();
});

test("a Helm suggestion that arrives on a later status read is filled in", async () => {
  // The first read hits a store error: no suggestion, no revision.
  defaults(unsaved({ health: "unavailable", suggestedTenantUrl: undefined }));
  const { unmount } = render(<RubrikSettings />);
  await settle();
  expect(tenantInput()?.value).toBe("");
  routes["GET /api/v1/rubrik/status"] = () => ok(status(unsaved()));
  await act(async () => {
    await reloadRubrikStatus();
  });
  await settle();
  expect(tenantInput()?.value).toBe(HELM_TENANT);
  expect(tenantNote()).not.toBeNull();
  unmount();
});

test("a late Helm suggestion never replaces what the admin typed", async () => {
  defaults(unsaved({ health: "unavailable", suggestedTenantUrl: undefined }));
  const { unmount } = render(<RubrikSettings />);
  await settle();
  fireEvent.change(tenantInput() as HTMLInputElement, {
    target: { value: "https://typed.my.rubrik.com" },
  });
  routes["GET /api/v1/rubrik/status"] = () => ok(status(unsaved()));
  await act(async () => {
    await reloadRubrikStatus();
  });
  await settle();
  expect(tenantInput()?.value).toBe("https://typed.my.rubrik.com");
  expect(tenantNote()).toBeNull();
  unmount();
});

test("saving the pre-filled form shows the saved URL without the Helm note", async () => {
  defaults(unsaved(), []);
  const saved = connection({
    tenantUrl: HELM_TENANT,
    clientId: "client-9",
    revision: 1,
    health: "unknown",
    lastSuccessAt: undefined,
  });
  routes["PUT /api/v1/rubrik/connection"] = () =>
    ok({ ...saved, mappingsCleared: false });
  const { unmount } = render(<RubrikSettings />);
  await settle();
  expect(tenantNote()).not.toBeNull();
  fireEvent.change(document.getElementById("rubrik-client-id") as HTMLElement, {
    target: { value: "client-9" },
  });
  fireEvent.change(secretInput(), { target: { value: SECRET } });
  // The status read after the save answers with the saved connection.
  routes["GET /api/v1/rubrik/status"] = () => ok(status(saved));
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Save connection" }));
  });
  await settle();
  expect(toasts.map((t) => t.message)).toEqual(["Rubrik connection saved"]);
  expect(tenantInput()?.value).toBe(HELM_TENANT);
  expect(tenantNote()).toBeNull();
  expect(secretInput().value).toBe("");
  body().getByText("Secret set.");
  unmount();
});
