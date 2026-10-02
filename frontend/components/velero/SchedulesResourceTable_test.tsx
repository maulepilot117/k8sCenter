/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { SchedulesResourceTable } from "@/components/velero/SchedulesResourceTable.tsx";
import type { Schedule } from "@/lib/velero-types.ts";

/**
 * Defect #5: the schedules table shows the outcome of each schedule's newest
 * backup in its own column, apart from "Last Backup" (Velero's
 * status.lastBackup), because the newest backup may be a manual trigger.
 */

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

let host: HTMLElement | null = null;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
});

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

function mount(schedules: Schedule[]): HTMLElement {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <SchedulesResourceTable
        schedules={schedules}
        deleting={null}
        onDelete={() => {}}
      />,
      host as HTMLElement,
    ),
  );
  return host;
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
