/** @jsxImportSource react */
import { expect, test } from "bun:test";
import type { Schedule } from "@/lib/velero-types.ts";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { SchedulesResourceTable } = await import("./SchedulesResourceTable.tsx");

function schedule(overrides: Partial<Schedule>): Schedule {
  return {
    name: "nightly",
    namespace: "velero",
    schedule: "0 1 * * *",
    paused: false,
    phase: "Enabled",
    ...overrides,
  } as Schedule;
}

function mount(schedules: Schedule[]): Element {
  return render(
    <SchedulesResourceTable
      schedules={schedules}
      deleting={null}
      onDelete={() => {}}
    />,
  ).container;
}

test("a schedule with a newest backup names its phase for screen readers", () => {
  const el = mount([
    schedule({ lastBackupPhase: "Failed", lastBackupOutcome: "failed" }),
  ]);
  const cell = el.querySelector('[title="Newest backup: Failed"]');
  expect(cell).not.toBeNull();
  expect(cell?.querySelector(".sr-only")?.textContent).toBe("Newest backup:");
  expect(cell?.textContent).toContain("Failed");
});

test("the newest-backup outcome is not paired with the cron-only age", () => {
  // A manual run before the first cron run: a phase, but no lastBackup.
  const el = mount([
    schedule({ lastBackupPhase: "Completed", lastBackupOutcome: "succeeded" }),
  ]);
  const cell = el.querySelector('[title="Newest backup: Completed"]');
  expect(cell?.textContent).not.toContain("Never");
  expect(el.textContent).toContain("Never");
});

test("a schedule without a newest backup shows no outcome", () => {
  const el = mount([schedule({})]);
  expect(el.querySelector('[title^="Newest backup"]')).toBeNull();
  expect(el.textContent).not.toContain("Newest backup:");
});
