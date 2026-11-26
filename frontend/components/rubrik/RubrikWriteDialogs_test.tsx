import { afterAll, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import type {
  RubrikNamespaceDetail,
  RubrikObjectDetail,
  RubrikOperation,
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
 * Rubrik write dialogs and job status (U14): controls follow the server's
 * flags, the snapshot SLA picker, the R15 object choice, the SLA-removal
 * notice, the restore confirmation, operation ids across retries, and the
 * job status poll. fetch is routed by method and URL; each test sets the
 * replies it needs.
 */
setupDom({ browser: true });

const { RubrikWriteControls, RubrikRestoreDialog } = await import(
  "./RubrikWriteDialogs.tsx"
);
const { RubrikJobStatus } = await import("./RubrikJobStatus.tsx");
const { default: RubrikDashboard } = await import(
  "@/src/islands/RubrikDashboard.tsx"
);
const { resetRubrikStatusForTest } = await import("@/src/lib/rubrik-status.ts");

const bunFetch = globalThis.fetch;
afterAll(() => {
  globalThis.fetch = bunFetch;
  resetRubrikStatusForTest();
});

const NOW = new Date().toISOString();

function object(over: Partial<RubrikObjectDetail> = {}): RubrikObjectDetail {
  return {
    family: "namespace",
    id: "obj-shop",
    name: "shop",
    coverage: { namespaces: ["shop"] },
    configuredSla: { id: "sla-gold", name: "Gold" },
    effectiveSla: { id: "sla-gold", name: "Gold" },
    compliance: "in_compliance",
    mayHoldClusterScoped: false,
    canSnapshot: true,
    canChangeSla: true,
    canRestore: true,
    snapshots: [],
    jobs: [],
    ...over,
  };
}

function detail(
  objects: RubrikObjectDetail[] = [object()],
  over: Partial<RubrikNamespaceDetail["namespace"]> = {},
): RubrikNamespaceDetail {
  const any = (k: "canSnapshot" | "canChangeSla" | "canRestore") =>
    objects.some((o) => o[k]);
  return {
    namespace: {
      namespace: "shop",
      protected: true,
      compliance: "in_compliance",
      objectIds: objects.map((o) => o.id),
      actionTargets: objects.map((o) => o.id),
      canSnapshot: any("canSnapshot"),
      canChangeSla: any("canChangeSla"),
      canRestore: any("canRestore"),
      ...over,
    },
    objects,
    freshness: { fetchedAt: NOW, stale: false },
  };
}

function operation(over: Partial<RubrikOperation> = {}): RubrikOperation {
  return {
    id: "op-1",
    kind: "snapshot",
    clusterId: "local",
    namespace: "shop",
    objectId: "obj-shop",
    state: "running",
    createdAt: NOW,
    updatedAt: NOW,
    ...over,
  };
}

const reply = (code: number, payload?: unknown): Response =>
  new Response(payload === undefined ? null : JSON.stringify(payload), {
    status: code,
    headers: { "Content-Type": "application/json" },
  });
const ok = (data: unknown, code = 200) => reply(code, { data });
const fail = (code: number, reason: string | undefined, message: string) =>
  reply(code, { error: { code, message, reason } });

interface Call {
  key: string;
  body: Record<string, unknown> | null;
}
let calls: Call[] = [];
let routes: Record<string, () => Response | Promise<Response>> = {};

beforeEach(() => {
  calls = [];
  routes = {
    "GET /api/v1/rubrik/sla-domains": () =>
      ok({
        noAccess: false,
        slaDomains: [
          { id: "sla-gold", name: "Gold", frequency: 0 },
          { id: "sla-silver", name: "Silver", frequency: 0 },
        ],
      }),
  };
  setAccessToken("t");
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    calls.push({
      key,
      body: init?.body ? JSON.parse(String(init.body)) : null,
    });
    const route = routes[key];
    return route ? route() : fail(599, "unstubbed", key);
  }) as unknown as typeof globalThis.fetch;
});

const sent = (key: string) => calls.filter((c) => c.key === key);
const click = async (name: string | RegExp) => {
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name }));
  });
  await settle();
};

function controls(d: RubrikNamespaceDetail, writesEnabled = true) {
  const started: RubrikOperation[] = [];
  const view = render(
    <RubrikWriteControls
      namespace="shop"
      detail={d}
      writesEnabled={writesEnabled}
      onStarted={(op) => started.push(op)}
    />,
  );
  return { started, view };
}

test("with writes disabled no write control renders", async () => {
  const { view } = controls(detail(), false);
  await settle();
  expect(body().queryByRole("button")).toBeNull();
  expect(body().queryByText(/Managed by an admin/)).toBeNull();
  view.unmount();
});

test("controls follow the server's flags: snapshot only, no SLA change or restore", async () => {
  // Through the page, so the restore control on each snapshot is covered.
  resetRubrikStatusForTest();
  const status: RubrikStatus = {
    enabled: true,
    preview: true,
    writesEnabled: true,
    cluster: { id: "local", state: "mapped" },
  };
  const d = detail([
    object({
      canChangeSla: false,
      canRestore: false,
      snapshots: [{ id: "s-1", time: NOW }],
    }),
  ]);
  routes["GET /api/v1/rubrik/status"] = () => ok(status);
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok({
      noAccess: false,
      namespaces: [d.namespace],
      objects: d.objects,
      freshness: d.freshness,
    });
  routes["GET /api/v1/rubrik/namespaces/shop"] = () => ok(d);
  const view = render(<RubrikDashboard />);
  await settle();
  await click("shop");
  body().getByRole("button", { name: "Take snapshot" });
  expect(body().queryByRole("button", { name: "Change SLA" })).toBeNull();
  expect(body().queryByRole("button", { name: /^Restore/ })).toBeNull();
  view.unmount();
  resetRubrikStatusForTest();
});

test("an object with no SLA domain asks for one before the snapshot can start", async () => {
  routes["POST /api/v1/rubrik/namespaces/shop/snapshots"] = () =>
    ok(operation(), 201);
  const { started, view } = controls(
    detail([object({ effectiveSla: { id: "UNPROTECTED", name: "" } })]),
  );
  await click("Take snapshot");
  const submit = () =>
    body()
      .getAllByRole("button", { name: "Take snapshot" })
      .at(-1) as HTMLButtonElement;
  const picker = body().getByLabelText(
    /This object has no SLA domain/,
  ) as HTMLSelectElement;
  expect(submit().disabled).toBe(true);
  fireEvent.change(picker, { target: { value: "sla-silver" } });
  expect(submit().disabled).toBe(false);
  await act(async () => {
    fireEvent.click(submit());
  });
  await settle();
  const post = sent("POST /api/v1/rubrik/namespaces/shop/snapshots")[0];
  expect(post?.body).toMatchObject({
    objectId: "obj-shop",
    slaId: "sla-silver",
  });
  expect(started.map((o) => o.id)).toEqual(["op-1"]);
  view.unmount();
});

test("a namespace covered by a multi-namespace set shows the admin note and no controls", async () => {
  const set = object({
    family: "protectionSet",
    id: "set-1",
    name: "tier",
    coverage: { namespaces: ["shop", "cart"] },
    canSnapshot: false,
    canChangeSla: false,
    canRestore: false,
  });
  const { view } = controls(detail([set]));
  await settle();
  body().getByText(/Managed by an admin/);
  expect(body().queryByRole("button")).toBeNull();
  view.unmount();
});

test("with two protecting objects the SLA change needs one picked and names it", async () => {
  routes["PUT /api/v1/rubrik/namespaces/shop/sla"] = () =>
    ok(operation({ kind: "sla_assign", state: "succeeded" }), 201);
  const a = object({
    family: "protectionSet",
    id: "set-a",
    name: "web",
    coverage: { namespaces: ["shop"] },
  });
  const b = object({
    family: "protectionSet",
    id: "set-b",
    name: "db",
    coverage: { namespaces: ["shop"] },
    effectiveSla: { id: "sla-silver", name: "Silver" },
  });
  const { view } = controls(detail([a, b]));
  await click("Change SLA");
  const submit = () =>
    body().getByRole("button", {
      name: "Change SLA domain",
    }) as HTMLButtonElement;
  // Each object is listed with its current SLA.
  body().getByLabelText("Protection set web · SLA Gold");
  body().getByLabelText("Protection set db · SLA Silver");
  expect(body().queryByLabelText(/New SLA domain/)).toBeNull();
  fireEvent.click(body().getByLabelText("Protection set db · SLA Silver"));
  fireEvent.change(body().getByLabelText(/New SLA domain/), {
    target: { value: "sla-gold" },
  });
  expect(body().getByTestId("rubrik-sla-change").textContent).toBe(
    "Silver → Gold",
  );
  await act(async () => {
    fireEvent.click(submit());
  });
  await settle();
  const put = sent("PUT /api/v1/rubrik/namespaces/shop/sla")[0];
  expect(put?.body).toMatchObject({ objectId: "set-b", slaId: "sla-gold" });
  view.unmount();
});

test("removing the SLA from one of two protecting objects says the other still protects", async () => {
  const a = object({
    family: "protectionSet",
    id: "set-a",
    name: "web",
    coverage: { namespaces: ["shop"] },
  });
  const b = object({
    family: "protectionSet",
    id: "set-b",
    name: "db",
    coverage: { namespaces: ["shop"] },
  });
  const { view } = controls(detail([a, b]));
  await click("Change SLA");
  fireEvent.click(body().getByLabelText("Protection set web · SLA Gold"));
  fireEvent.change(body().getByLabelText(/New SLA domain/), {
    target: { value: "__none__" },
  });
  const dialog = body().getByRole("dialog");
  expect(dialog.textContent).toContain("still protected by protection set db");
  expect(dialog.textContent).not.toContain("Protection of shop stops");
  view.unmount();
});

test("removing the only object's SLA says protection stops", async () => {
  const { view } = controls(detail());
  await click("Change SLA");
  fireEvent.change(body().getByLabelText(/New SLA domain/), {
    target: { value: "__none__" },
  });
  body().getByText(/Protection of shop stops/);
  body().getByRole("button", { name: "Remove SLA domain" });
  view.unmount();
});

test("a retry after a network error reuses the operation id; a new request does not", async () => {
  let attempt = 0;
  routes["PUT /api/v1/rubrik/namespaces/shop/sla"] = () => {
    attempt++;
    if (attempt === 1) throw new TypeError("network down");
    return ok(operation({ kind: "sla_assign", state: "succeeded" }), 201);
  };
  const { started, view } = controls(detail());
  await click("Change SLA");
  fireEvent.change(body().getByLabelText(/New SLA domain/), {
    target: { value: "sla-silver" },
  });
  await click("Change SLA domain");
  body().getByText(/did not reach k8sCenter/);
  await click("Change SLA domain");
  const puts = sent("PUT /api/v1/rubrik/namespaces/shop/sla");
  expect(puts).toHaveLength(2);
  expect(puts[0]?.body?.operationId).toBe(puts[1]?.body?.operationId);
  expect(started).toHaveLength(1);
  view.unmount();

  // A different request gets a different id.
  calls = [];
  attempt = 0;
  const again = controls(detail());
  await click("Change SLA");
  fireEvent.change(body().getByLabelText(/New SLA domain/), {
    target: { value: "sla-silver" },
  });
  await click("Change SLA domain");
  fireEvent.change(body().getByLabelText(/New SLA domain/), {
    target: { value: "__none__" },
  });
  await click("Remove SLA domain");
  const [first, second] = sent("PUT /api/v1/rubrik/namespaces/shop/sla");
  expect(first?.body?.operationId).not.toBe(second?.body?.operationId);
  again.view.unmount();
});

test("an operation already in flight says it is already running", async () => {
  routes["POST /api/v1/rubrik/namespaces/shop/snapshots"] = () =>
    fail(
      409,
      "operation_in_flight",
      "another Rubrik operation on this object is still running",
    );
  const { started, view } = controls(detail());
  await click("Take snapshot");
  await act(async () => {
    fireEvent.click(
      body()
        .getAllByRole("button", { name: "Take snapshot" })
        .at(-1) as HTMLElement,
    );
  });
  await settle();
  expect(body().getByRole("alert").textContent).toContain("already running");
  expect(started).toHaveLength(0);
  view.unmount();
});

test("the restore confirm stays disabled until the target namespace is typed exactly", async () => {
  routes[
    "GET /api/v1/rubrik/namespaces/shop/restore-targets?objectId=obj-shop"
  ] = () =>
    ok({
      objectId: "obj-shop",
      sourceNamespace: "shop",
      inPlaceOnly: false,
      targets: ["shop", "shop-copy"],
      overwriteNotice:
        "Existing objects in the target namespace with the same names may be replaced.",
    });
  routes["POST /api/v1/rubrik/restores"] = () =>
    ok(operation({ kind: "restore" }), 201);
  const started: RubrikOperation[] = [];
  const view = render(
    <RubrikRestoreDialog
      namespace="shop"
      object={object()}
      snapshot={{ id: "snap-1", time: NOW }}
      onStarted={(op) => started.push(op)}
      onClose={() => {}}
    />,
  );
  await settle();
  body().getByText(/may be replaced/);
  fireEvent.change(body().getByLabelText("Restore into"), {
    target: { value: "shop-copy" },
  });
  await click("Continue");
  const confirm = () =>
    body().getByRole("button", { name: "Restore" }) as HTMLButtonElement;
  const input = body().getByRole("textbox");
  expect(confirm().disabled).toBe(true);
  fireEvent.change(input, { target: { value: "shop" } });
  expect(confirm().disabled).toBe(true);
  fireEvent.change(input, { target: { value: "shop-copy" } });
  expect(confirm().disabled).toBe(false);
  await act(async () => {
    fireEvent.click(confirm());
  });
  await settle();
  const post = sent("POST /api/v1/rubrik/restores")[0];
  expect(post?.body).toMatchObject({
    objectId: "obj-shop",
    snapshotId: "snap-1",
    targetNamespace: "shop-copy",
  });
  expect(typeof post?.body?.operationId).toBe("string");
  expect(started).toHaveLength(1);
  view.unmount();
});

test("a refused restore shows the server's reason back in the form", async () => {
  routes[
    "GET /api/v1/rubrik/namespaces/shop/restore-targets?objectId=obj-shop"
  ] = () =>
    ok({
      objectId: "obj-shop",
      sourceNamespace: "shop",
      inPlaceOnly: false,
      targets: ["shop"],
      overwriteNotice: "Existing objects may be replaced.",
    });
  routes["POST /api/v1/rubrik/restores"] = () =>
    fail(
      403,
      "missing_permission",
      "your own Kubernetes permissions do not cover this restore; missing: create secrets",
    );
  const view = render(
    <RubrikRestoreDialog
      namespace="shop"
      object={object()}
      snapshot={{ id: "snap-1", time: NOW }}
      onStarted={() => {}}
      onClose={() => {}}
    />,
  );
  await settle();
  await click("Continue");
  fireEvent.change(body().getByRole("textbox"), { target: { value: "shop" } });
  await click("Restore");
  expect(body().getByRole("alert").textContent).toContain(
    "missing: create secrets",
  );
  body().getByLabelText("Restore into");
  view.unmount();
});

/**
 * Lets the poll run: each short act() commits what the last read changed, so
 * the next read is scheduled before the next one.
 */
async function wait(ms: number) {
  for (let left = ms; left > 0; left -= 10) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 10));
    });
  }
}

test("job status polls a running operation and stops when it settles as partial", async () => {
  let reads = 0;
  routes["GET /api/v1/rubrik/operations/op-1"] = () => {
    reads++;
    return ok(
      operation(
        reads < 2
          ? {}
          : { state: "partial", message: "2 of 3 resources were restored" },
      ),
    );
  };
  const settledOps: RubrikOperation[] = [];
  const view = render(
    <RubrikJobStatus
      operation={operation()}
      pollMs={5}
      onSettled={(op) => settledOps.push(op)}
    />,
  );
  body().getByText("Running");
  await wait(80);
  const status = body().getByTestId("rubrik-op-op-1");
  expect(status.textContent).toContain("Partial");
  expect(status.textContent).not.toContain("Succeeded");
  expect(status.textContent).toContain("2 of 3 resources were restored");
  expect(settledOps.map((o) => o.state)).toEqual(["partial"]);
  const after = reads;
  await wait(40);
  expect(reads).toBe(after);
  view.unmount();
});

test("an operation read that turns 404 stops polling and says it is no longer visible", async () => {
  let reads = 0;
  routes["GET /api/v1/rubrik/operations/op-1"] = () => {
    reads++;
    return fail(404, undefined, "operation not found");
  };
  const view = render(<RubrikJobStatus operation={operation()} pollMs={5} />);
  await wait(60);
  body().getByText(/no longer visible to you/);
  expect(reads).toBe(1);
  await wait(40);
  expect(reads).toBe(1);
  view.unmount();
});
