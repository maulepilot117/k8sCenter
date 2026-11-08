/** @jsxImportSource react */
import { expect, mock, test } from "bun:test";
// Loads preact/compat, as ToastProvider does on every ChromeLayout page.
import "react";
import { useState } from "react";
import type {
  AssuranceException,
  AssurancePolicy,
  AssuranceRuntime,
  AssuranceStatus,
} from "@/lib/backup-assurance-types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { ExceptionList } = await import(
  "@/components/velero/AssuranceExceptions.tsx"
);
const {
  emptyForm,
  formFromPolicy,
  localFieldErrors,
  PolicyEditor,
  PolicyTable,
} = await import("@/components/velero/AssurancePolicies.tsx");
const { ConditionCounts, FreshnessRow, RuntimePanel } = await import(
  "@/components/velero/AssuranceStatusPanels.tsx"
);

function exception(over: Partial<AssuranceException> = {}): AssuranceException {
  return {
    id: "ex-1",
    policyId: "pol-1",
    subject: {
      kind: "schedule",
      namespace: "velero",
      name: "nightly",
      uid: "u",
    },
    condition: "overdue",
    state: "open",
    severity: "critical",
    openedAt: "2026-01-01T00:00:00Z",
    lastObservedAt: "2026-01-02T00:00:00Z",
    resolvedAt: null,
    observationCount: 1,
    lastSuccessAt: null,
    subjectStatus: "found",
    detail: { expectedRunKnown: false, lastOutcome: "partial" },
    ...over,
  };
}

function policy(over: Partial<AssurancePolicy> = {}): AssurancePolicy {
  return {
    id: "pol-1",
    scopeKind: "schedule",
    scopeNamespace: "velero",
    scopeName: "nightly",
    maxAgeSeconds: 90000,
    graceSeconds: 3600,
    treatPartialAs: "failure",
    alertOnPaused: true,
    enabled: true,
    createdBy: "a",
    createdAt: "2026-01-01T00:00:00Z",
    updatedBy: "a",
    updatedAt: null,
    revision: 3,
    scheduleStatus: "found",
    ...over,
  };
}

const listProps = {
  exState: "open" as const,
  exceptions: [] as AssuranceException[],
  total: 0,
  offset: 0,
  loading: false,
  failure: null,
  state: "ok" as const,
  policyCount: 1,
  onSwitchState: () => {},
  onChangePage: () => {},
};

test("ExceptionList renders a grouped card with chips and the partial note", () => {
  render(
    <ExceptionList
      {...listProps}
      total={1}
      exceptions={[
        exception({
          detail: {
            expectedRunKnown: false,
            lastOutcome: "partial",
            failureReason: "boom",
          },
        }),
      ]}
    />,
  );
  const v = body();
  expect(v.getByText("Overdue (1)")).toBeTruthy();
  expect(v.getByText("schedule velero/nightly")).toBeTruthy();
  expect(v.getByTestId("assurance-condition-chip").className).toContain(
    "bg-warning-dim",
  );
  expect(v.getByText("critical").className).toContain("bg-error-dim");
  expect(v.getByTestId("assurance-partial-success-chip")).toBeTruthy();
  expect(v.getByText("no successful backup observed")).toBeTruthy();
  expect(v.getByText("boom")).toBeTruthy();
  expect(v.getByText("1 observation", { exact: false })).toBeTruthy();
});

test("ExceptionList empty states depend on state, policies and tab", () => {
  const { rerender } = render(<ExceptionList {...listProps} />);
  expect(body().getByTestId("assurance-no-open")).toBeTruthy();
  rerender(<ExceptionList {...listProps} state="stale" />);
  expect(body().getByTestId("assurance-no-open-unknown").textContent).toContain(
    "collection is stale",
  );
  rerender(<ExceptionList {...listProps} policyCount={0} />);
  expect(body().queryByTestId("assurance-no-open")).toBeNull();
  rerender(<ExceptionList {...listProps} exState="resolved" />);
  expect(body().getByText("No resolved exceptions are retained.")).toBeTruthy();
});

test("ExceptionList shows forbidden, failure, and loading branches", () => {
  const { rerender } = render(
    <ExceptionList {...listProps} failure={{ status: 403, message: "x" }} />,
  );
  expect(body().getByTestId("assurance-forbidden")).toBeTruthy();
  rerender(
    <ExceptionList
      {...listProps}
      failure={{ status: 500, message: "kaput" }}
    />,
  );
  expect(body().getByRole("alert").textContent).toContain("kaput");
  rerender(<ExceptionList {...listProps} loading />);
  expect(body().queryByTestId("assurance-no-open")).toBeNull();
});

test("ExceptionList toggle and paging fire callbacks", () => {
  const onSwitchState = mock((_s: string) => {});
  const onChangePage = mock((_d: number) => {});
  const rows = Array.from({ length: 2 }, (_, i) => exception({ id: `e${i}` }));
  render(
    <ExceptionList
      {...listProps}
      total={120}
      offset={50}
      exceptions={rows}
      onSwitchState={onSwitchState}
      onChangePage={onChangePage}
    />,
  );
  const v = body();
  expect(v.getByText("51–52 of 120")).toBeTruthy();
  expect(
    v.getByRole("button", { name: "Open" }).getAttribute("aria-pressed"),
  ).toBe("true");
  fireEvent.click(v.getByRole("button", { name: "Resolved" }));
  expect(onSwitchState).toHaveBeenCalledWith("resolved");
  fireEvent.click(v.getByRole("button", { name: "Next" }));
  fireEvent.click(v.getByRole("button", { name: "Previous" }));
  expect(onChangePage.mock.calls).toEqual([[1], [-1]]);
});

test("PolicyTable renders rows and fires edit/delete; busy disables", () => {
  const p = policy({ scheduleStatus: "not_found" });
  const onEdit = mock((_p: AssurancePolicy) => {});
  const onDelete = mock((_p: AssurancePolicy) => {});
  const { rerender } = render(
    <PolicyTable
      policies={[p]}
      busy={false}
      onEdit={onEdit}
      onDelete={onDelete}
    />,
  );
  const v = body();
  expect(v.getByText("1d 1h")).toBeTruthy();
  expect(v.getByText("as failure")).toBeTruthy();
  expect(v.getByText("alert")).toBeTruthy();
  expect(v.getByText("enabled")).toBeTruthy();
  expect(v.getByText(/this policy currently watches nothing/)).toBeTruthy();
  fireEvent.click(v.getByRole("button", { name: "Edit" }));
  fireEvent.click(v.getByRole("button", { name: "Delete" }));
  expect(onEdit).toHaveBeenCalledWith(p);
  expect(onDelete).toHaveBeenCalledWith(p);
  rerender(
    <PolicyTable policies={[p]} busy onEdit={onEdit} onDelete={onDelete} />,
  );
  expect(
    (body().getByRole("button", { name: "Delete" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});

/** Holds the form as the island does and validates before calling back. */
function Harness({
  editing,
  onValid,
}: {
  editing: AssurancePolicy | null;
  onValid: (f: ReturnType<typeof emptyForm>) => void;
}) {
  const [form, setForm] = useState(
    editing ? formFromPolicy(editing) : emptyForm(),
  );
  const [errs, setErrs] = useState<Record<string, string>>({});
  return (
    <PolicyEditor
      editing={editing}
      form={form}
      fieldErrors={errs}
      formError={null}
      conflict={false}
      saving={false}
      onChange={(patch) => setForm((f) => ({ ...f, ...patch }))}
      onSubmit={() => {
        const e = localFieldErrors(form, editing === null);
        setErrs(e);
        if (Object.keys(e).length === 0) onValid(form);
      }}
      onCancel={() => {}}
      onReload={() => {}}
    />
  );
}

test("PolicyEditor: invalid submit shows errors and does not call back", () => {
  const onValid = mock((_f: unknown) => {});
  render(<Harness editing={null} onValid={onValid} />);
  const v = body();
  fireEvent.click(v.getByRole("button", { name: "Create policy" }));
  expect(onValid).not.toHaveBeenCalled();
  expect(v.getByText("is required")).toBeTruthy();
  expect(v.getByText("is required for schedule scope")).toBeTruthy();
});

test("PolicyEditor: fill and submit passes the typed form", async () => {
  const onValid = mock((_f: unknown) => {});
  render(<Harness editing={null} onValid={onValid} />);
  const v = body();
  await act(async () => {
    fireEvent.input(v.getByLabelText("Namespace"), {
      target: { value: "velero" },
    });
    fireEvent.input(v.getByLabelText("Schedule"), {
      target: { value: "nightly" },
    });
    fireEvent.input(v.getByLabelText("Maximum age (minutes)"), {
      target: { value: "120" },
    });
    fireEvent.change(v.getByLabelText("Treat PartiallyFailed as"), {
      target: { value: "success" },
    });
    fireEvent.click(v.getByLabelText("Enabled"));
  });
  expect(v.getByText(/2h without a successful backup/)).toBeTruthy();
  await act(async () => {
    fireEvent.click(v.getByRole("button", { name: "Create policy" }));
  });
  expect(onValid).toHaveBeenCalledTimes(1);
  expect(onValid.mock.calls[0][0]).toEqual({
    scopeKind: "schedule",
    scopeNamespace: "velero",
    scopeName: "nightly",
    maxAgeMinutes: "120",
    graceMinutes: "60",
    treatPartialAs: "success",
    alertOnPaused: true,
    enabled: false,
  });
});

test("PolicyEditor edit mode locks scope; error and conflict reload render", () => {
  const onReload = mock(() => {});
  const p = policy();
  render(
    <PolicyEditor
      editing={p}
      form={formFromPolicy(p)}
      fieldErrors={{}}
      formError="changed elsewhere"
      conflict
      saving={false}
      onChange={() => {}}
      onSubmit={() => {}}
      onCancel={() => {}}
      onReload={onReload}
    />,
  );
  const v = body();
  expect(v.getByText("Edit policy for schedule velero/nightly")).toBeTruthy();
  expect((v.getByLabelText("Namespace") as HTMLInputElement).disabled).toBe(
    true,
  );
  expect(v.getByTestId("assurance-policy-form-error").textContent).toContain(
    "changed elsewhere",
  );
  expect(
    (v.getByRole("button", { name: "Save changes" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  fireEvent.click(v.getByRole("button", { name: "Reload policy" }));
  expect(onReload).toHaveBeenCalledTimes(1);
});

const counts = (over: Record<string, number> = {}) =>
  ({
    enabled: true,
    collection: "ok",
    collectionSource: "this_replica",
    policyCount: 1,
    open: {
      total: 2,
      byCondition: {
        overdue: 2,
        failed: 0,
        partially_failed: 0,
        paused: 0,
        never_run: 0,
        location_unavailable: 0,
        collection_unknown: 0,
        ...over,
      },
    },
  }) as AssuranceStatus;

test("ConditionCounts shows zeros as numbers when current, unknown otherwise", () => {
  const { rerender } = render(<ConditionCounts status={counts()} state="ok" />);
  expect(
    body().getByTestId("assurance-count-failed").getAttribute("data-unknown"),
  ).toBe("false");
  expect(body().getByTestId("assurance-count-overdue").textContent).toContain(
    "2",
  );
  rerender(<ConditionCounts status={counts()} state="stale" />);
  expect(
    body().getByTestId("assurance-count-failed").getAttribute("data-unknown"),
  ).toBe("true");
  expect(
    body().getByTestId("assurance-count-overdue").getAttribute("data-unknown"),
  ).toBe("false");
  expect(body().getByTestId("assurance-count-failed").className).toContain(
    "opacity-70",
  );
});

test("FreshnessRow shows the state word, class and Velero-missing note", () => {
  render(
    <FreshnessRow state="stale" status={counts()} veleroDetected={false} />,
  );
  const v = body();
  expect(v.getByTestId("assurance-state-word").textContent).toBe("stale");
  expect(v.getByTestId("assurance-state-word").className).toContain(
    "bg-warning-dim",
  );
  expect(
    v.getByTestId("assurance-surface-state").getAttribute("data-state"),
  ).toBe("stale");
  expect(
    v.getByText("judged from this replica's last collection"),
  ).toBeTruthy();
  expect(v.getByTestId("assurance-velero-not-detected")).toBeTruthy();
});

test("RuntimePanel renders collector facts and last error", () => {
  const runtime: AssuranceRuntime = {
    holder: "pod-a",
    lastTickAt: null,
    lastRunAt: null,
    lastCollection: "failed",
    findingCount: 0,
    lastError: "no route",
    lastErrorAt: "2026-01-01T00:00:00Z",
    leaseHeld: true,
    lease: {
      holder: "pod-b",
      fence: 1,
      expiresAt: "2026-01-01T00:00:00Z",
      expired: true,
    },
    deliveryBacklog: { pending: 4, failed: 1 },
  };
  render(<RuntimePanel runtime={runtime} />);
  const t = body().getByTestId("assurance-runtime").textContent ?? "";
  expect(t).toContain("pod-a");
  expect(t).toContain("never (failed)");
  expect(t).toContain("pod-b (expired) — held by this replica");
  expect(t).toContain("4 pending, 1 failed");
  expect(t).toContain("no route");
});
