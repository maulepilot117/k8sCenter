/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// Bun's own fetch, captured before happy-dom replaces it.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { useStorageClasses } = await import(
  "@/lib/hooks/use-storage-classes.ts"
);
const { default: PVCWizard } = await import("./PVCWizard.tsx");
const { default: StatefulSetWizard } = await import("./StatefulSetWizard.tsx");
const { default: SnapshotWizard } = await import("./SnapshotWizard.tsx");
const { default: RestoreSnapshotWizard } = await import(
  "./RestoreSnapshotWizard.tsx"
);

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];

/** What the stubbed API returns; each test may override a field. */
let storageClassesBody: unknown;
let snapshotClassesBody: unknown;
let pvcsBody: unknown;
let storageClassesFail = false;

const STORAGE_CLASSES = [
  { metadata: { name: "fast" }, provisioner: "csi.fast" },
  { metadata: { name: "slow" }, provisioner: "csi.slow" },
];

function json(data: unknown, extra: Record<string, unknown> = {}): Response {
  return new Response(JSON.stringify({ data, ...extra }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  calls = [];
  storageClassesFail = false;
  storageClassesBody = STORAGE_CLASSES;
  snapshotClassesBody = {
    data: [],
    metadata: { available: true },
  };
  pvcsBody = [];
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({
      url,
      method: init?.method ?? "GET",
      body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
    });
    if (url.includes("/v1/resources/storageclasses")) {
      if (storageClassesFail) throw new Error("network down");
      return json(storageClassesBody);
    }
    if (url.endsWith("/v1/resources/namespaces")) {
      return json([{ metadata: { name: "default" } }]);
    }
    if (url.endsWith("/v1/storage/snapshot-classes")) {
      return json(snapshotClassesBody);
    }
    if (url.includes("/v1/resources/pvcs/")) return json(pvcsBody);
    return json({ yaml: "kind: Preview\n" });
  }) as typeof fetch;
});

afterEach(() => {
  setAccessToken(null);
});

afterAll(() => {
  globalThis.fetch = bunFetch;
});

function type(el: HTMLElement, value: string) {
  fireEvent.input(el, { target: { value } });
}

function choose(el: HTMLElement, value: string) {
  fireEvent.change(el, { target: { value } });
}

function click(el: HTMLElement) {
  fireEvent.click(el);
}

async function next() {
  await act(async () => {
    click(body().getByRole("button", { name: /^Continue/ }));
  });
}

function previewCall(kind: string): Call | undefined {
  return calls.find((c) => c.url.endsWith(`/v1/wizards/${kind}/preview`));
}

async function previewBody(kind: string) {
  await waitFor(() => expect(previewCall(kind)).toBeDefined());
  return previewCall(kind)?.body as Record<string, unknown>;
}

/** The <select> whose options include `optionText`. */
function selectWith(optionText: string): HTMLSelectElement {
  const found = (body().getAllByRole("combobox") as HTMLSelectElement[]).find(
    (s) => Array.from(s.options).some((o) => o.value === optionText),
  );
  if (!found) throw new Error(`no select offers ${optionText}`);
  return found;
}

function optionValues(select: HTMLSelectElement): string[] {
  return Array.from(select.options).map((o) => o.value);
}

function leaveIsBlocked(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(event);
  return event.defaultPrevented;
}

// ------------------------------------------------------- useStorageClasses

function StorageClassProbe() {
  const storageClasses = useStorageClasses();
  return (
    <ul data-testid="sc">
      {storageClasses.map((sc) => (
        <li key={sc.metadata.name}>{sc.metadata.name}</li>
      ))}
    </ul>
  );
}

const probeNames = () => {
  const list = body().getByTestId("sc") as HTMLElement;
  return Array.from(list.querySelectorAll("li")).map((li) => li.textContent);
};

test("useStorageClasses: fills the list from the storage class endpoint", async () => {
  render(<StorageClassProbe />);
  expect(probeNames()).toEqual([]);
  await waitFor(() => expect(probeNames()).toEqual(["fast", "slow"]));
  expect(
    calls.some((c) => c.url.endsWith("/v1/resources/storageclasses?limit=500")),
  ).toBe(true);
});

test("useStorageClasses: a non-array response leaves the list empty", async () => {
  storageClassesBody = { not: "a list" };
  render(<StorageClassProbe />);
  await waitFor(() =>
    expect(calls.some((c) => c.url.includes("storageclasses"))).toBe(true),
  );
  await act(async () => {});
  expect(probeNames()).toEqual([]);
});

test("useStorageClasses: a failed request leaves the list empty", async () => {
  storageClassesFail = true;
  render(<StorageClassProbe />);
  await waitFor(() =>
    expect(calls.some((c) => c.url.includes("storageclasses"))).toBe(true),
  );
  await act(async () => {});
  expect(probeNames()).toEqual([]);
});

// --------------------------------------------------------------------- PVC

async function loadedPVC() {
  render(<PVCWizard onClose={() => {}} />);
  // The first storage class is selected once the list arrives.
  await waitFor(() => expect(selectWith("fast").value).toBe("fast"));
}

test("PVC: the storage class dropdown is filled from the hook and defaults to the first", async () => {
  await loadedPVC();
  expect(optionValues(selectWith("fast"))).toEqual(["", "fast", "slow"]);
});

test("PVC: a name then Next advances to the review step", async () => {
  await loadedPVC();
  body().getByText("Step 1 of 2", { exact: false });
  type(body().getByPlaceholderText("e.g. my-data"), "data-vol");
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
  expect(body().queryByPlaceholderText("e.g. my-data")).toBeNull();
});

test("PVC: an empty name shows its error and does not advance", async () => {
  await loadedPVC();
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("pvc")).toBeUndefined();
});

test("PVC: clearing the storage class blocks Next with a field error", async () => {
  await loadedPVC();
  type(body().getByPlaceholderText("e.g. my-data"), "data-vol");
  choose(selectWith("fast"), "");
  await next();
  body().getByText("Required");
  expect(previewCall("pvc")).toBeUndefined();
});

test("PVC: the review step posts the typed values", async () => {
  await loadedPVC();
  type(body().getByPlaceholderText("e.g. my-data"), "typed-vol");
  choose(selectWith("slow"), "slow");
  type(body().getByPlaceholderText("10"), "25");
  click(body().getByRole("button", { name: "Ti" }));
  click(body().getByRole("button", { name: "ReadWriteMany" }));
  await next();
  const sent = await previewBody("pvc");
  expect(sent.name).toBe("typed-vol");
  expect(sent.storageClassName).toBe("slow");
  expect(sent.size).toBe("25Ti");
  expect(sent.accessMode).toBe("ReadWriteMany");
});

test("PVC: a non-positive size blocks Next", async () => {
  await loadedPVC();
  type(body().getByPlaceholderText("e.g. my-data"), "data-vol");
  type(body().getByPlaceholderText("10"), "0");
  await next();
  body().getByText("Must be a positive number");
  expect(previewCall("pvc")).toBeUndefined();
});

// ------------------------------------------------------------- StatefulSet

async function statefulSetToContainerStep() {
  render(<StatefulSetWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-statefulset"), "db");
  await next();
  await waitFor(() => body().getByPlaceholderText("nginx:latest"));
}

test("StatefulSet: name and the derived headless service then Next advances", async () => {
  render(<StatefulSetWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-statefulset"), "db");
  expect(
    (body().getByPlaceholderText("my-statefulset-headless") as HTMLInputElement)
      .value,
  ).toBe("db-headless");
  await next();
  await waitFor(() => body().getByPlaceholderText("nginx:latest"));
  expect(body().queryByPlaceholderText("my-statefulset")).toBeNull();
});

test("StatefulSet: an empty name shows its error and does not advance", async () => {
  render(<StatefulSetWizard onClose={() => {}} />);
  await next();
  body().getAllByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByPlaceholderText("my-statefulset");
  expect(previewCall("statefulset")).toBeUndefined();
});

test("StatefulSet: an empty image blocks the container step", async () => {
  await statefulSetToContainerStep();
  await next();
  body().getByText("Required");
  body().getByPlaceholderText("nginx:latest");
  expect(previewCall("statefulset")).toBeUndefined();
});

test("StatefulSet: typing marks the wizard dirty", () => {
  render(<StatefulSetWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("my-statefulset"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("StatefulSet: a volume claim template row offers the hook's storage classes and posts its values", async () => {
  await statefulSetToContainerStep();
  type(body().getByPlaceholderText("nginx:latest"), "postgres:16");
  click(body().getByRole("button", { name: "+ Add Volume Claim Template" }));
  const classSelect = await waitFor(() => selectWith("slow"));
  expect(optionValues(classSelect)).toEqual(["", "fast", "slow"]);
  // A row added after the list arrived defaults to the first class.
  expect(classSelect.value).toBe("fast");
  type(body().getByPlaceholderText("data"), "pgdata");
  choose(classSelect, "slow");
  fireEvent.input(body().getByDisplayValue("10"), { target: { value: "50" } });
  choose(body().getByDisplayValue("Gi"), "Ti");
  click(body().getByRole("radio", { name: /ReadWriteMany/ }));
  await next();
  const sent = await previewBody("statefulset");
  expect(sent.name).toBe("db");
  expect(sent.volumeClaimTemplates).toEqual([
    {
      name: "pgdata",
      storageClassName: "slow",
      size: "50Ti",
      accessMode: "ReadWriteMany",
    },
  ]);
  expect((sent.container as { volumeMounts: unknown }).volumeMounts).toEqual([
    { name: "pgdata", mountPath: "/data/pgdata" },
  ]);
});

test("StatefulSet: a volume claim template without a name blocks Next", async () => {
  await statefulSetToContainerStep();
  type(body().getByPlaceholderText("nginx:latest"), "postgres:16");
  click(body().getByRole("button", { name: "+ Add Volume Claim Template" }));
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  expect(previewCall("statefulset")).toBeUndefined();
});

test("StatefulSet: removing a volume claim template keeps the other rows' values", async () => {
  await statefulSetToContainerStep();
  for (let i = 0; i < 3; i++) {
    click(body().getByRole("button", { name: "+ Add Volume Claim Template" }));
  }
  const names = () =>
    body().getAllByPlaceholderText("data") as HTMLInputElement[];
  type(names()[0], "a");
  type(names()[1], "b");
  type(names()[2], "c");
  click(body().getAllByTitle("Remove volume")[1]);
  expect(names().map((n) => n.value)).toEqual(["a", "c"]);
});

// ---------------------------------------------------------------- Snapshot

const PVCS = [
  {
    metadata: { name: "bound-data" },
    spec: { storageClassName: "fast" },
    status: { phase: "Bound" },
  },
  {
    metadata: { name: "pending-data" },
    spec: { storageClassName: "slow" },
    status: { phase: "Pending" },
  },
  {
    metadata: { name: "bound-other" },
    spec: { storageClassName: "slow" },
    status: { phase: "Bound" },
  },
];

async function loadedSnapshot() {
  pvcsBody = PVCS;
  snapshotClassesBody = {
    data: [
      {
        name: "snap-fast",
        driver: "csi.fast",
        deletionPolicy: "Delete",
        isDefault: true,
      },
      {
        name: "snap-slow",
        driver: "csi.slow",
        deletionPolicy: "Delete",
        isDefault: false,
      },
    ],
    metadata: { available: true },
  };
  render(<SnapshotWizard onClose={() => {}} />);
  await waitFor(() => selectWith("bound-data"));
}

test("Snapshot: the source PVC list offers only Bound claims", async () => {
  await loadedSnapshot();
  expect(optionValues(selectWith("bound-data"))).toEqual([
    "",
    "bound-data",
    "bound-other",
  ]);
});

test("Snapshot: choosing a source PVC fills the name and Next advances", async () => {
  await loadedSnapshot();
  choose(selectWith("bound-data"), "bound-data");
  const name = body().getByPlaceholderText(
    "e.g. my-pvc-snap-20260323-120000",
  ) as HTMLInputElement;
  expect(name.value).toMatch(/^bound-data-snap-\d{8}-\d{6}$/);
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
});

test("Snapshot: no source PVC shows its error and does not advance", async () => {
  await loadedSnapshot();
  await next();
  body().getAllByText("Required");
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("snapshot")).toBeUndefined();
});

test("Snapshot: snapshot classes are filtered to the source PVC's provisioner", async () => {
  await loadedSnapshot();
  expect(optionValues(selectWith("snap-fast"))).toEqual([
    "",
    "snap-fast",
    "snap-slow",
  ]);
  choose(selectWith("bound-other"), "bound-other");
  await waitFor(() =>
    expect(optionValues(selectWith("snap-slow"))).toEqual(["", "snap-slow"]),
  );
});

test("Snapshot: the review step posts the typed values", async () => {
  await loadedSnapshot();
  choose(selectWith("bound-data"), "bound-data");
  choose(selectWith("snap-fast"), "snap-fast");
  type(
    body().getByPlaceholderText("e.g. my-pvc-snap-20260323-120000"),
    "typed-snap",
  );
  await next();
  const sent = await previewBody("snapshot");
  expect(sent).toEqual({
    name: "typed-snap",
    namespace: "default",
    sourcePVC: "bound-data",
    volumeSnapshotClassName: "snap-fast",
  });
});

test("Snapshot: a preselected PVC seeds the source and name", async () => {
  pvcsBody = PVCS;
  render(
    <SnapshotWizard
      onClose={() => {}}
      preselectedNs="default"
      preselectedPvc="bound-data"
    />,
  );
  await waitFor(() =>
    expect(selectWith("bound-data").value).toBe("bound-data"),
  );
  const name = body().getByPlaceholderText(
    "e.g. my-pvc-snap-20260323-120000",
  ) as HTMLInputElement;
  expect(name.value).toMatch(/^bound-data-snap-/);
});

test("Snapshot: shows a notice when the snapshot CRDs are not installed", async () => {
  snapshotClassesBody = { data: [], metadata: { available: false } };
  render(<SnapshotWizard onClose={() => {}} />);
  await waitFor(() => body().getByText("VolumeSnapshot CRDs Not Installed"));
});

// ----------------------------------------------------------------- Restore

const SNAPSHOT = {
  ns: "default",
  name: "db-snap",
  restoreSize: "20Gi",
  snapshotClass: "snap-fast",
};

async function loadedRestore() {
  render(
    <RestoreSnapshotWizard onClose={() => {}} snapshotParams={SNAPSHOT} />,
  );
  await waitFor(() => selectWith("fast"));
}

test("Restore: the PVC name defaults from the snapshot and a storage class then Next advances", async () => {
  await loadedRestore();
  expect(
    (body().getByPlaceholderText("e.g. my-data-restore") as HTMLInputElement)
      .value,
  ).toBe("db-snap-restore");
  choose(selectWith("fast"), "fast");
  await next();
  await waitFor(() => expect(previewCall("pvc")).toBeDefined());
  expect(body().queryByPlaceholderText("e.g. my-data-restore")).toBeNull();
});

test("Restore: no storage class shows its error and does not advance", async () => {
  await loadedRestore();
  await next();
  body().getByText("Required");
  expect(body().getByPlaceholderText("e.g. my-data-restore")).toBeTruthy();
  expect(previewCall("pvc")).toBeUndefined();
});

test("Restore: a size below the snapshot's restore size is rejected", async () => {
  await loadedRestore();
  choose(selectWith("fast"), "fast");
  type(body().getByPlaceholderText("10"), "5");
  await next();
  body().getByText("Must be at least 20Gi (snapshot restore size)");
  expect(previewCall("pvc")).toBeUndefined();
});

test("Restore: the review step posts the typed values and the snapshot as data source", async () => {
  await loadedRestore();
  type(body().getByPlaceholderText("e.g. my-data-restore"), "typed-restore");
  choose(selectWith("slow"), "slow");
  type(body().getByPlaceholderText("10"), "40");
  await next();
  const sent = await previewBody("pvc");
  expect(sent.name).toBe("typed-restore");
  expect(sent.storageClassName).toBe("slow");
  expect(sent.size).toBe("40Gi");
  expect(sent.dataSource).toEqual({
    name: "db-snap",
    kind: "VolumeSnapshot",
    apiGroup: "snapshot.storage.k8s.io",
  });
});

test("Restore: without snapshot information it shows the missing-information notice", () => {
  render(<RestoreSnapshotWizard onClose={() => {}} snapshotParams={null} />);
  body().getByText("Missing snapshot information");
});

test("Restore: shows a notice when the snapshot CRDs are not installed", async () => {
  snapshotClassesBody = { metadata: { available: false } };
  render(
    <RestoreSnapshotWizard onClose={() => {}} snapshotParams={SNAPSHOT} />,
  );
  await waitFor(() => body().getByText("VolumeSnapshot CRDs Not Installed"));
});
