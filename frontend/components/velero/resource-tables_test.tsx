import { expect, test } from "bun:test";
import type { RubrikNamespaceList, RubrikObject } from "@/lib/rubrik-types.ts";
import type { Backup, Restore } from "@/lib/velero-types.ts";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { BackupsResourceTable, rubrikBackupRows } = await import(
  "./BackupsResourceTable.tsx"
);
const { RestoresResourceTable } = await import("./RestoresResourceTable.tsx");

function backup(overrides: Partial<Backup>): Backup {
  return {
    name: "nightly-1",
    namespace: "velero",
    phase: "Completed",
    snapshotVolumes: true,
    itemsBackedUp: 40,
    totalItems: 42,
    warnings: 0,
    errors: 0,
    ...overrides,
  };
}

function restore(overrides: Partial<Restore>): Restore {
  return {
    name: "restore-1",
    namespace: "velero",
    phase: "Completed",
    itemsRestored: 10,
    totalItems: 12,
    warnings: 0,
    errors: 0,
    ...overrides,
  };
}

function headers(): string[] {
  return body()
    .getAllByRole("columnheader")
    .map((h: HTMLElement) => h.textContent ?? "");
}

function cellsOfRow(index: number): HTMLElement[] {
  const rows = body().getAllByRole("row");
  // rows[0] is the header row
  return Array.from(rows[index + 1].querySelectorAll('[role="cell"]'));
}

test("backups: headers, cells and healthy chip for a completed backup", () => {
  render(
    <BackupsResourceTable
      backups={[
        backup({ scheduleName: "nightly", startTime: "2020-01-01T00:00:00Z" }),
      ]}
      deleting={null}
      onDelete={() => {}}
    />,
  );
  expect(headers()).toEqual([
    "Name",
    "Provider",
    "Status",
    "Schedule",
    "Started",
    "Items",
    "Issues",
    "",
  ]);
  const cells = cellsOfRow(0);
  expect(cells[0].textContent).toBe("nightly-1velero");
  expect(cells[1].textContent).toBe("Velero");
  expect(cells[2].textContent).toBe("Completed");
  expect((cells[2].firstElementChild as HTMLElement).style.color).toBe(
    "var(--success)",
  );
  expect(cells[3].textContent).toBe("nightly");
  expect(cells[4].textContent).not.toBe("—");
  expect(cells[4].textContent).not.toBe("");
  expect(cells[5].textContent).toBe("40/42");
  expect(cells[6].textContent).toBe("0");
});

test("backups: missing schedule and start time show a dash, issues show counts", () => {
  render(
    <BackupsResourceTable
      backups={[backup({ phase: "Failed", warnings: 2, errors: 3 })]}
      deleting={null}
      onDelete={() => {}}
    />,
  );
  const cells = cellsOfRow(0);
  expect((cells[2].firstElementChild as HTMLElement).style.color).toBe(
    "var(--error)",
  );
  expect(cells[3].textContent).toBe("—");
  expect(cells[4].textContent).toBe("—");
  expect(cells[6].textContent).toBe("2W/3E");
});

test("backups: empty state", () => {
  render(
    <BackupsResourceTable backups={[]} deleting={null} onDelete={() => {}} />,
  );
  expect(body().getByText("No backups found.")).toBeTruthy();
  expect(body().queryByRole("table")).toBeNull();
});

test("backups: Delete sends namespace and name, Restore links to the backup", () => {
  const calls: Array<[string, string]> = [];
  render(
    <BackupsResourceTable
      backups={[backup({ name: "b1", namespace: "ns1" })]}
      deleting={null}
      onDelete={(ns, name) => calls.push([ns, name])}
    />,
  );
  fireEvent.click(body().getByRole("button", { name: "Delete" }));
  expect(calls).toEqual([["ns1", "b1"]]);
  const link = body().getByRole("link");
  expect(link.getAttribute("href")).toBe("/backup/restores/new?backup=b1");
  expect(link.textContent).toBe("Restore");
});

test("backups: the row being deleted shows an ellipsis and is disabled", () => {
  render(
    <BackupsResourceTable
      backups={[
        backup({ name: "b1", namespace: "ns1" }),
        backup({ name: "b2", namespace: "ns1" }),
      ]}
      deleting="backup-ns1-b1"
      onDelete={() => {}}
    />,
  );
  const buttons = body().getAllByRole("button") as HTMLButtonElement[];
  const deletes = buttons.filter(
    (b) => b.textContent === "…" || b.textContent === "Delete",
  );
  expect(deletes.map((b) => b.textContent)).toEqual(["…", "Delete"]);
  expect(deletes[0].disabled).toBe(true);
  expect(deletes[1].disabled).toBe(false);
});

test("restores: headers and cells, backup falls back to schedule then dash", () => {
  render(
    <RestoresResourceTable
      restores={[
        restore({ backupName: "b1", startTime: "2020-01-01T00:00:00Z" }),
        restore({ name: "r2", scheduleName: "nightly", phase: "Failed" }),
        restore({
          name: "r3",
          phase: "PartiallyFailed",
          warnings: 1,
          errors: 4,
        }),
      ]}
    />,
  );
  expect(headers().slice(0, 6)).toEqual([
    "Name",
    "Status",
    "Backup",
    "Started",
    "Items",
    "Issues",
  ]);
  const first = cellsOfRow(0);
  expect(first[0].textContent).toBe("restore-1velero");
  expect(first[1].textContent).toBe("Completed");
  expect((first[1].firstElementChild as HTMLElement).style.color).toBe(
    "var(--success)",
  );
  expect(first[2].textContent).toBe("b1");
  expect(first[3].textContent).not.toBe("—");
  expect(first[4].textContent).toBe("10/12");
  expect(first[5].textContent).toBe("0");
  const second = cellsOfRow(1);
  expect(second[2].textContent).toBe("nightly");
  expect(second[3].textContent).toBe("—");
  expect((second[1].firstElementChild as HTMLElement).style.color).toBe(
    "var(--error)",
  );
  const third = cellsOfRow(2);
  expect(third[2].textContent).toBe("—");
  expect(third[5].textContent).toBe("1W/4E");
});

test("restores: empty state", () => {
  render(<RestoresResourceTable restores={[]} />);
  expect(body().getByText("No restores found.")).toBeTruthy();
  expect(body().queryByRole("table")).toBeNull();
});

// ---------------------------------------------------------------------------
// Rubrik rows

function rubrikObject(over: Partial<RubrikObject> = {}): RubrikObject {
  return {
    family: "namespace",
    id: "obj-shop",
    name: "shop",
    coverage: { namespaces: ["shop"] },
    configuredSla: { id: "sla-gold", name: "Gold" },
    effectiveSla: { id: "sla-gold", name: "Gold" },
    compliance: "in_compliance",
    newestSnapshot: "2020-01-01T00:00:00Z",
    mayHoldClusterScoped: false,
    canSnapshot: false,
    canChangeSla: false,
    canRestore: false,
    ...over,
  };
}

function rubrikList(objects: RubrikObject[]): RubrikNamespaceList {
  return {
    noAccess: false,
    namespaces: [
      {
        namespace: "shop",
        protected: true,
        compliance: "in_compliance",
        objectIds: ["obj-shop"],
        actionTargets: ["obj-shop"],
        canSnapshot: false,
        canChangeSla: false,
        canRestore: false,
      },
    ],
    objects,
  };
}

test("rubrikBackupRows: one row per visible object, with SLA, kind and where to open it", () => {
  const rows = rubrikBackupRows({
    kind: "loaded",
    list: rubrikList([
      rubrikObject(),
      rubrikObject({
        family: "protectionSet",
        id: "set-1",
        name: "tier",
        coverage: { namespaces: ["cart", "pay"] },
        effectiveSla: { id: "", name: "" },
        compliance: "out_of_compliance",
        relic: true,
      }),
    ]),
  });
  expect(rows).toEqual([
    {
      id: "obj-shop",
      name: "shop",
      kind: "Namespace",
      sla: "Gold",
      compliance: "in_compliance",
      newestSnapshot: "2020-01-01T00:00:00Z",
      relic: false,
      panelNamespace: "shop",
    },
    {
      id: "set-1",
      name: "tier",
      kind: "Protection set · covers cart, pay",
      sla: "No SLA",
      compliance: "out_of_compliance",
      newestSnapshot: "2020-01-01T00:00:00Z",
      relic: true,
      // No namespace row the user can see names the set.
      panelNamespace: undefined,
    },
  ]);
});

test("rubrikBackupRows: anything but a loaded answer, or no access, has no rows", () => {
  expect(rubrikBackupRows({ kind: "loading" })).toEqual([]);
  expect(rubrikBackupRows({ kind: "disabled" })).toEqual([]);
  expect(
    rubrikBackupRows({
      kind: "loaded",
      list: { ...rubrikList([rubrikObject()]), noAccess: true },
    }),
  ).toEqual([]);
});

test("backups: Rubrik rows sit with the Velero rows, a provider each", () => {
  const opened: string[] = [];
  const rows = rubrikBackupRows({
    kind: "loaded",
    list: rubrikList([
      rubrikObject({ compliance: "out_of_compliance" }),
      rubrikObject({
        family: "protectionSet",
        id: "set-1",
        name: "tier",
        coverage: { all: true },
      }),
    ]),
  });
  render(
    <BackupsResourceTable
      backups={[backup({})]}
      deleting={null}
      onDelete={() => {}}
      rubrik={rows}
      onOpenRubrik={(ns) => opened.push(ns)}
    />,
  );
  expect(cellsOfRow(0)[1].textContent).toBe("Velero");
  const shop = cellsOfRow(1);
  expect(shop[0].textContent).toBe("shopNamespace");
  expect(shop[1].textContent).toBe("Rubrik");
  expect(shop[2].textContent).toBe("Out of compliance");
  expect(shop[3].textContent).toBe("SLA Gold");
  expect(shop[4].textContent).not.toBe("—");
  expect(cellsOfRow(2)[0].textContent).toBe(
    "tierProtection set · covers every namespace",
  );
  // Only the object a visible namespace names can be opened.
  const open = body().getAllByRole("button", { name: /^Snapshots of / });
  expect(open.map((b: HTMLElement) => b.getAttribute("aria-label"))).toEqual([
    "Snapshots of shop",
  ]);
  fireEvent.click(open[0] as HTMLElement);
  expect(opened).toEqual(["shop"]);
});

test("backups: Rubrik rows alone are not an empty table", () => {
  render(
    <BackupsResourceTable
      backups={[]}
      deleting={null}
      onDelete={() => {}}
      rubrik={rubrikBackupRows({
        kind: "loaded",
        list: rubrikList([rubrikObject()]),
      })}
    />,
  );
  expect(body().queryByText("No backups found.")).toBeNull();
  expect(cellsOfRow(0)[1].textContent).toBe("Rubrik");
  // Without a handler there is nothing to open.
  expect(body().queryByRole("button", { name: /^Snapshots of / })).toBeNull();
});
