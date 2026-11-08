/** @jsxImportSource react */
import { expect, mock, test } from "bun:test";
import type { OwnershipState, RepairState } from "@/lib/change-tracking.ts";
import type {
  ApplyTracking,
  OwnershipView,
  ReceiptState,
  VerificationState,
} from "@/lib/change-types.ts";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { ChangeStateBadges } = await import("./ChangeStateBadges.tsx");
const { OwnershipList } = await import("./OwnershipList.tsx");
const { OwnershipSection } = await import("./OwnershipSection.tsx");
const { RepairBanner } = await import("./RepairBanner.tsx");
const { TrackedApplyPanel } = await import("./TrackedApplyPanel.tsx");

const OP = "6f1d3c52-4b1e-4f0a-9c53-0d7a2b8e1f64";

function view(
  kind: string,
  name: string,
  over: Partial<OwnershipView> = {},
): OwnershipView {
  return {
    object: { kind, name, namespace: "shop" },
    controller: "none",
    confidence: "unavailable",
    reason: "no-controller-installed",
    identityBasis: "group-kind-namespace-name",
    uidConfirmed: false,
    writableGitSource: false,
    observedAt: "2026-01-01T00:00:00Z",
    ...over,
  } as OwnershipView;
}

function tracking(over: Partial<ApplyTracking> = {}): ApplyTracking {
  return {
    operationId: OP,
    receiptUrl: `/api/v1/changes/${OP}`,
    state: "applied",
    clusterId: "local",
    clusterGeneration: "local",
    contentDigest: "abc",
    recordedThrough: 2,
    notAttempted: 0,
    unrecorded: 0,
    replayed: false,
    containsSecret: false,
    objects: [],
    verification: { state: "pending", url: "" },
    warnings: [],
    ...over,
  } as ApplyTracking;
}

test("ChangeStateBadges shows execution and verification as two badges", () => {
  const { container } = render(
    <ChangeStateBadges
      state={"applied" satisfies ReceiptState}
      verification={"pending" satisfies VerificationState}
    />,
  );
  const wrapper = container.firstElementChild as HTMLElement;
  expect(wrapper.className).toBe("inline-flex flex-wrap items-center gap-1.5");
  expect(wrapper.textContent).toBe("Applied·Verification pending");
  const sep = wrapper.querySelector("[aria-hidden='true']");
  expect(sep?.className).toBe("text-text-muted");
});

test("ChangeStateBadges keeps a failed verification apart from a good apply", () => {
  const { container } = render(
    <ChangeStateBadges state="applied" verification="verification_failed" />,
  );
  expect(container.textContent).toContain("Applied");
  expect(container.textContent).toContain("Verification failed");
});

test("OwnershipList renders one row per object with its badge and text", () => {
  const { container } = render(
    <OwnershipList
      results={[
        view("Deployment", "web"),
        view("Service", "web", {
          confidence: "forbidden",
          reason: "apps-redacted",
        }),
      ]}
    />,
  );
  const list = body().getByRole("list", { name: "GitOps ownership" });
  expect(list.className).toContain("list-none");
  const rows = container.querySelectorAll("li");
  expect(rows).toHaveLength(2);
  expect(rows[0]?.className).toBe("flex flex-col gap-1 px-3.5 py-2.5 text-sm");
  expect(rows[0]?.textContent).toContain("Deployment shop/web");
  expect(rows[0]?.textContent).toContain("No controller");
  expect(rows[0]?.querySelector("p")?.className).toBe(
    "m-0 text-text-secondary",
  );
  expect(rows[1]?.textContent).toContain("Hidden");
});

test("OwnershipList takes a custom label and copes with a repeated object", () => {
  render(
    <OwnershipList
      label="Previewed objects"
      results={[view("Deployment", "web"), view("Deployment", "web")]}
    />,
  );
  const list = body().getByRole("list", { name: "Previewed objects" });
  expect(list.querySelectorAll("li")).toHaveLength(2);
});

test("OwnershipSection renders nothing while idle", () => {
  const { container } = render(<OwnershipSection state={{ status: "idle" }} />);
  expect(container.innerHTML).toBe("");
});

test("OwnershipSection shows loading and error as status lines", () => {
  const loading = render(<OwnershipSection state={{ status: "loading" }} />);
  const p = body().getByRole("status");
  expect(p.textContent).toBe(
    "Checking which GitOps controllers manage these objects…",
  );
  expect(p.className).toBe("m-0 text-sm text-text-muted");
  loading.unmount();

  render(<OwnershipSection state={{ status: "error", message: "No luck" }} />);
  const err = body().getByRole("status");
  expect(err.textContent).toBe("No luck");
  expect(err.className).toBe("m-0 text-sm text-warning");
});

test("OwnershipSection ready lists results and says how many were not checked", () => {
  const state: OwnershipState = {
    status: "ready",
    results: [view("Deployment", "web")],
    omitted: 3,
  };
  const first = render(<OwnershipSection state={state} />);
  expect(
    body().getByRole("heading", { name: "GitOps ownership" }).className,
  ).toBe("m-0 text-sm font-semibold text-text-primary");
  expect(body().getByRole("list")).toBeTruthy();
  expect(first.container.textContent).toContain(
    "Ownership was checked for the first 50 objects only; 3 more were not checked.",
  );
  first.unmount();

  const one = render(<OwnershipSection state={{ ...state, omitted: 1 }} />);
  expect(one.container.textContent).toContain("1 more was not checked.");
  one.unmount();

  const none = render(<OwnershipSection state={{ ...state, omitted: 0 }} />);
  expect(none.container.textContent).not.toContain("not checked");
});

const clusterLabel = (id: string) =>
  id === "" || id === "local" ? "Local" : id;

function banner(
  repair: RepairState,
  over: Partial<{
    tracked: boolean;
    selected: string;
    onStop: () => void;
  }> = {},
) {
  return render(
    <RepairBanner
      repair={repair}
      tracked={over.tracked ?? true}
      onStop={over.onStop ?? (() => {})}
      selectedClusterId={over.selected ?? "local"}
      clusterLabel={clusterLabel}
    />,
  );
}

test("RepairBanner renders nothing without a repair link", () => {
  const { container } = banner({ status: "none" });
  expect(container.innerHTML).toBe("");
});

test("RepairBanner warns when the repair link is invalid", () => {
  banner({ status: "invalid" });
  expect(body().getByRole("status").textContent).toContain(
    "The repair link does not name a valid change",
  );
});

test("RepairBanner links to the original receipt and stops on request", () => {
  const onStop = mock(() => {});
  banner({ status: "valid", id: OP }, { onStop });
  const status = body().getByRole("status");
  expect(status.className).toBe("flex flex-col gap-2");
  expect(status.textContent).toContain("Repairing change 6f1d3c52.");
  expect(status.textContent).not.toContain("is recorded only while");
  const link = body().getByRole("link", { name: "Open the original receipt" });
  expect(link.getAttribute("href")).toBe(`/changes/${OP}`);
  expect(link.className).toBe("font-medium text-inherit underline");
  fireEvent.click(body().getByRole("button", { name: "Stop repairing" }));
  expect(onStop).toHaveBeenCalledTimes(1);
});

test("RepairBanner explains that an untracked apply is not linked", () => {
  banner({ status: "valid", id: OP }, { tracked: false });
  expect(body().getByRole("status").textContent).toContain(
    "The link to the original change is recorded only while",
  );
});

test("RepairBanner warns when the viewed cluster differs from the original", () => {
  banner({ status: "valid", id: OP, clusterId: "prod" }, { selected: "local" });
  const text = body().getByRole("status").textContent ?? "";
  expect(text).toContain("The original change was applied to prod");
  expect(text).toContain("but you are viewing Local");
  expect(text).toContain("Switch clusters before validating to retry on prod.");
});

test("RepairBanner stays quiet about clusters when they match", () => {
  banner(
    { status: "valid", id: OP, clusterId: "local" },
    { selected: "local" },
  );
  expect(body().getByRole("status").textContent).not.toContain(
    "The original change was applied to",
  );
});

test("TrackedApplyPanel renders nothing for an untracked apply with no record", () => {
  const { container } = render(
    <TrackedApplyPanel tracking={undefined} sentOperationId={null} />,
  );
  expect(container.innerHTML).toBe("");
});

test("TrackedApplyPanel says so when a requested record never came back", () => {
  render(<TrackedApplyPanel tracking={undefined} sentOperationId={OP} />);
  expect(body().getByRole("status").textContent).toBe(
    "This apply was not recorded: the server returned no change record.",
  );
});

test("TrackedApplyPanel shows the receipt link and both badges", () => {
  const { container } = render(
    <TrackedApplyPanel tracking={tracking()} sentOperationId={OP} />,
  );
  const section = body().getByRole("region", { name: "Change record" });
  expect(section.className).toContain("rounded-lg");
  expect(section.textContent).toContain("Change recorded");
  expect(section.textContent).toContain("Applied");
  expect(section.textContent).toContain("Verification pending");
  const link = body().getByRole("link", {
    name: "View change receipt 6f1d3c52",
  });
  expect(link.getAttribute("href")).toBe(`/changes/${OP}`);
  expect(link.className).toBe("font-medium text-accent");
  expect(container.querySelectorAll("p")).toHaveLength(0);
});

test("TrackedApplyPanel accepts a differently-cased operation id as the same", () => {
  render(
    <TrackedApplyPanel
      tracking={tracking({ operationId: OP.toUpperCase() })}
      sentOperationId={OP}
    />,
  );
  expect(body().queryByText(/different operation id/)).toBeNull();
});

test("TrackedApplyPanel lists every qualifier of the outcome", () => {
  const other = "11111111-4b1e-4f0a-9c53-0d7a2b8e1f64";
  render(
    <TrackedApplyPanel
      tracking={tracking({
        operationId: other,
        state: "partial",
        replayed: true,
        notAttempted: 2,
        unrecorded: 1,
        containsSecret: true,
        warnings: ["Recording slowed down"],
      })}
      sentOperationId={OP}
    />,
  );
  const text = body().getByRole("region", { name: "Change record" })
    .textContent as string;
  expect(text).toContain("Some objects changed and some did not.");
  expect(text).toContain("names a different operation id");
  expect(text).toContain("Nothing was applied a second time.");
  expect(text).toContain("2 documents were not attempted");
  expect(text).toContain("They were never sent to the cluster");
  expect(text).toContain("1 document has no recorded outcome");
  expect(text).toContain("The cluster may already hold it");
  expect(text).toContain("This change includes a Secret.");
  const warnings = body().getByRole("list", { name: "Recording warnings" });
  expect(warnings.textContent).toBe("Recording slowed down");
  expect(warnings.className).toBe("m-0 pl-5 text-warning");
});

test("TrackedApplyPanel uses the singular for one document not attempted", () => {
  render(
    <TrackedApplyPanel
      tracking={tracking({ notAttempted: 1 })}
      sentOperationId={OP}
    />,
  );
  const text = body().getByRole("region", { name: "Change record" })
    .textContent as string;
  expect(text).toContain("1 document was not attempted");
  expect(text).toContain("It was never sent to the cluster");
});
