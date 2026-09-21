import { describe, expect, test } from "bun:test";
import { rollUpDiagnostics, severityOf } from "./diagnostics-summary.ts";

// The severity roll-up the namespace diagnostics widget renders.
//
// The payload is a flat list of failing pods with a reason string each, and
// the card shows counts by severity -- so the mapping from reason to severity
// and the counting are the two things that can be wrong in a way an operator
// acts on. Both are pure, so both live here rather than inside the component
// (D-10).

describe("severityOf", () => {
  test("a back-off reason is critical", () => {
    expect(severityOf("CrashLoopBackOff")).toBe("critical");
    expect(severityOf("ImagePullBackOff")).toBe("critical");
    expect(severityOf("ErrImagePull")).toBe("critical");
  });

  test("Pending is a warning", () => {
    // A pod waiting to be scheduled is a real finding and not the same
    // finding as one that cannot start at all.
    expect(severityOf("Pending")).toBe("warning");
  });

  test("a reason this build does not know is critical, never a warning", () => {
    // The backend decides what counts as failing; this only decides how
    // loudly to say so. Defaulting to the milder of the two would make a
    // failure mode added on the server read as less serious here, which is
    // the same "absence rendered as good news" the availability states exist
    // to prevent.
    expect(severityOf("SomeFutureReason")).toBe("critical");
    expect(severityOf("")).toBe("critical");
  });
});

describe("rollUpDiagnostics", () => {
  test("counts by severity and by reason", () => {
    const out = rollUpDiagnostics({
      total: 10,
      failing: [
        { kind: "Pod", name: "a", reason: "CrashLoopBackOff" },
        { kind: "Pod", name: "b", reason: "CrashLoopBackOff" },
        { kind: "Pod", name: "c", reason: "Pending" },
      ],
    });
    expect(out.total).toBe(10);
    expect(out.failing).toBe(3);
    expect(out.healthy).toBe(7);
    expect(out.critical).toBe(2);
    expect(out.warning).toBe(1);
    expect(out.reasons).toEqual([
      { reason: "CrashLoopBackOff", count: 2, severity: "critical" },
      { reason: "Pending", count: 1, severity: "warning" },
    ]);
  });

  test("reasons are ordered by count, then alphabetically", () => {
    // Stable, so the card does not reshuffle between two refreshes that
    // happen to return the same counts in a different order.
    const out = rollUpDiagnostics({
      total: 4,
      failing: [
        { kind: "Pod", name: "a", reason: "Pending" },
        { kind: "Pod", name: "b", reason: "ErrImagePull" },
        { kind: "Pod", name: "c", reason: "CrashLoopBackOff" },
        { kind: "Pod", name: "d", reason: "CrashLoopBackOff" },
      ],
    });
    expect(out.reasons.map((r) => r.reason)).toEqual([
      "CrashLoopBackOff",
      "ErrImagePull",
      "Pending",
    ]);
  });

  test("a healthy namespace is not empty", () => {
    // The distinction the whole widget turns on: a namespace with workloads
    // and nothing wrong is good news, and a namespace with no workloads at
    // all is not news -- it is the shape a deleted namespace comes back as.
    const out = rollUpDiagnostics({ total: 6, failing: [] });
    expect(out.empty).toBe(false);
    expect(out.healthy).toBe(6);
    expect(out.failing).toBe(0);
  });

  test("a namespace with no workloads is empty, not healthy", () => {
    const out = rollUpDiagnostics({ total: 0, failing: [] });
    expect(out.empty).toBe(true);
  });

  test("a missing or malformed payload reads as empty rather than healthy", () => {
    // `Failing` is `omitempty`-free on the Go side and a nil slice is
    // normalized to `[]`, but the widget must not depend on that: a body it
    // cannot read renders as "nothing to show" rather than as a clean bill of
    // health for a namespace nobody checked.
    expect(rollUpDiagnostics(null).empty).toBe(true);
    expect(rollUpDiagnostics({} as never).empty).toBe(true);
    expect(
      rollUpDiagnostics({ total: 3, failing: null as never }).failing,
    ).toBe(0);
  });

  test("healthy never goes negative", () => {
    // `total` counts pods and `failing` counts failing pods, so the two can
    // only disagree if the payload does -- but a negative "healthy" rendered
    // in a card is worse than a zero.
    expect(
      rollUpDiagnostics({
        total: 1,
        failing: [
          { kind: "Pod", name: "a", reason: "Pending" },
          { kind: "Pod", name: "b", reason: "Pending" },
        ],
      }).healthy,
    ).toBe(0);
  });
});
