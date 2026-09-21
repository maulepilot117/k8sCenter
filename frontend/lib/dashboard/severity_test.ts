import { describe, expect, test } from "bun:test";
import {
  COMPLIANCE_HISTORY_DAYS,
  COMPLIANCE_PAGE_HREF,
  compareSeverity,
  normalizeSeverity,
  policyComplianceView,
  policyViolationsView,
  SEVERITY_ORDER,
  severityBreakdown,
  severityRank,
  UNRANKED_SEVERITY,
  VIOLATIONS_PAGE_HREF,
  VULNERABILITIES_PAGE_HREF,
  vulnerabilitySeverityView,
  workloadHref,
} from "./severity.ts";

// The derived state behind `policy-compliance`, `policy-violations` and
// `vulnerability-severity` -- the security family.
//
// The judgement these three share is what a severity label is worth. A
// violations list and a CVE roll-up that disagreed about whether "high"
// outranks "medium", or about where an unrecognised label belongs, would be
// two cards on one dashboard contradicting each other. So the ranking lives in
// one module, under test, and the components only render what it answers
// (D-10, KTD8).
//
// The direction every function errs in is the dashboard's direction: a payload
// this build cannot read resolves to "we could not read it", never to an empty
// list, and a count of zero is reported as a count of zero rather than as
// absence. Absence is the shell's business -- a family that is not installed
// never reaches these functions at all, because `resolveWidgetState` returns
// `unavailable` before `render` is called (KTD1).

// --------------------------------------------------------------------------
// Severity primitives
// --------------------------------------------------------------------------

describe("severity ranking", () => {
  test("the four known labels rank critical, high, medium, low", () => {
    expect([...SEVERITY_ORDER]).toEqual(["critical", "high", "medium", "low"]);
    const ranks = SEVERITY_ORDER.map((s) => severityRank(s));
    expect(ranks).toEqual([0, 1, 2, 3]);
  });

  test("an unrecognised severity sorts last rather than throwing", () => {
    // The failure this prevents: Kyverno and Gatekeeper both let a policy
    // author write any string into a severity annotation, and Trivy emits
    // "UNKNOWN" for a CVE with no assigned rating. A lookup that threw, or one
    // that returned NaN, would either blank the card or sort the unrated
    // finding to the top of a ranking of what to fix first.
    expect(severityRank("informational")).toBe(SEVERITY_ORDER.length);
    expect(severityRank("")).toBe(SEVERITY_ORDER.length);
    expect(severityRank(undefined)).toBe(SEVERITY_ORDER.length);
    expect(severityRank(null)).toBe(SEVERITY_ORDER.length);
    expect(severityRank(7)).toBe(SEVERITY_ORDER.length);
    expect(severityRank({ severity: "critical" })).toBe(SEVERITY_ORDER.length);
  });

  test("case and surrounding space do not change a label's rank", () => {
    // Trivy reports "CRITICAL"; Kyverno annotations are conventionally
    // lowercase. The two describe the same thing and must rank the same.
    expect(severityRank("CRITICAL")).toBe(0);
    expect(severityRank("  High  ")).toBe(1);
    expect(normalizeSeverity("CRITICAL")).toBe("critical");
    expect(normalizeSeverity("  High  ")).toBe("high");
    expect(normalizeSeverity("   ")).toBe(UNRANKED_SEVERITY);
    expect(normalizeSeverity(42)).toBe(UNRANKED_SEVERITY);
  });

  test("compareSeverity orders critical above high above medium above low", () => {
    const shuffled = ["low", "critical", "medium", "high"];
    expect([...shuffled].sort(compareSeverity)).toEqual([
      "critical",
      "high",
      "medium",
      "low",
    ]);
  });

  test("compareSeverity puts every unrecognised label after every known one", () => {
    const mixed = ["informational", "low", "zebra", "critical"];
    expect([...mixed].sort(compareSeverity)).toEqual([
      "critical",
      "low",
      // Two unranked labels tie on rank, so the tiebreak is the label itself.
      // Any total order will do; it only has to be the SAME order every
      // render, or the card reshuffles on a refresh that changed nothing.
      "informational",
      "zebra",
    ]);
  });
});

describe("severityBreakdown", () => {
  test("always reports the four known buckets, including the empty ones", () => {
    // Zero is a fact about the cluster and has to be representable: a card
    // that only listed non-zero buckets could not tell "no criticals" from
    // "nothing was counted".
    const buckets = severityBreakdown(["high", "high", "low"]);
    expect(buckets).toEqual([
      { severity: "critical", count: 0 },
      { severity: "high", count: 2 },
      { severity: "medium", count: 0 },
      { severity: "low", count: 1 },
    ]);
  });

  test("an unrecognised label gets its own bucket, appended after the four", () => {
    const buckets = severityBreakdown(["informational", "critical", null]);
    expect(buckets.map((b) => b.severity)).toEqual([
      "critical",
      "high",
      "medium",
      "low",
      "informational",
      UNRANKED_SEVERITY,
    ]);
    expect(buckets.find((b) => b.severity === "informational")?.count).toBe(1);
    expect(buckets.find((b) => b.severity === UNRANKED_SEVERITY)?.count).toBe(
      1,
    );
  });

  test("an empty input is the four zero buckets, not a throw", () => {
    expect(severityBreakdown([])).toEqual([
      { severity: "critical", count: 0 },
      { severity: "high", count: 0 },
      { severity: "medium", count: 0 },
      { severity: "low", count: 0 },
    ]);
  });
});

// --------------------------------------------------------------------------
// policy-compliance
// --------------------------------------------------------------------------

/** `GET /v1/policies/compliance`, which answers with the handler's
 * `ComplianceScore`. */
function score(over: Record<string, unknown> = {}): unknown {
  return {
    scope: "",
    score: 82.5,
    pass: 33,
    fail: 5,
    warn: 2,
    total: 40,
    bySeverity: {
      critical: { pass: 4, fail: 1, total: 5 },
      medium: { pass: 29, fail: 4, total: 33 },
    },
    ...over,
  };
}

describe("policyComplianceView", () => {
  test("reads the server's score, counts and per-severity failures", () => {
    const view = policyComplianceView(score(), [
      { date: "2026-08-21", score: 71 },
      { date: "2026-09-20", score: 82.5 },
    ]);
    expect(view.readable).toBe(true);
    expect(view.score).toBe(82.5);
    expect(view.pass).toBe(33);
    expect(view.fail).toBe(5);
    expect(view.warn).toBe(2);
    expect(view.total).toBe(40);
    expect(view.governed).toBe(true);
    // Rank order, not the map's key order, and the buckets a policy set never
    // populates are still reported so the reader can see they are empty.
    expect(view.failuresBySeverity).toEqual([
      { severity: "critical", count: 1 },
      { severity: "high", count: 0 },
      { severity: "medium", count: 4 },
      { severity: "low", count: 0 },
    ]);
  });

  test("an unreadable payload is unreadable, never a zero score", () => {
    // A zero here would render as total non-compliance -- an alarm about a
    // cluster whose compliance we simply could not read. `readable: false` is
    // what lets the card say which of the two it is.
    for (const payload of [null, undefined, "nope", 7, [], {}]) {
      expect(policyComplianceView(payload, null).readable).toBe(false);
    }
    expect(policyComplianceView({ score: "82.5" }, null).readable).toBe(false);
    expect(policyComplianceView({ score: Number.NaN }, null).readable).toBe(
      false,
    );
  });

  test("an engine with no policies is ungoverned, not fully compliant", () => {
    // `computeCompliance` in backend/internal/policy/handler.go returns
    // score 100 when nothing is weighted, which is arithmetically right and
    // editorially a lie: a cluster running Kyverno with no ClusterPolicy is
    // not a compliant cluster, it is an unpoliced one. The gauge must not
    // render that 100.
    const view = policyComplianceView(
      score({
        score: 100,
        pass: 0,
        fail: 0,
        warn: 0,
        total: 0,
        bySeverity: {},
      }),
      null,
    );
    expect(view.readable).toBe(true);
    expect(view.governed).toBe(false);
    expect(view.total).toBe(0);
  });

  test("a score outside 0..100 is clamped so the gauge cannot overdraw", () => {
    expect(policyComplianceView(score({ score: 140 }), null).score).toBe(100);
    expect(policyComplianceView(score({ score: -3 }), null).score).toBe(0);
  });

  test("history absent is the widget's own degraded state, not an error", () => {
    // `/v1/policies/compliance/history` is admin-gated and answers 503 when
    // the deployment has no database, so a non-admin and a databaseless
    // install both get nothing back. The CURRENT score is still present and
    // still worth rendering, which is why the history read is declared
    // optional -- the shell resolves the widget `ready` and this flag is what
    // the card uses to drop the trend line rather than the whole gauge.
    const view = policyComplianceView(score(), null);
    expect(view.readable).toBe(true);
    expect(view.score).toBe(82.5);
    expect(view.history.available).toBe(false);
    expect(view.history.delta).toBeNull();
  });

  test("history present reports the change across the window", () => {
    const view = policyComplianceView(score({ score: 90 }), [
      { date: "2026-08-21", score: 70 },
      { date: "2026-09-01", score: 80 },
      { date: "2026-09-20", score: 90 },
    ]);
    expect(view.history.available).toBe(true);
    expect(view.history.delta).toBe(20);
    expect(view.history.days).toBe(COMPLIANCE_HISTORY_DAYS);
  });

  test("a history window with fewer than two readable points has no delta", () => {
    // One snapshot is a point, not a trend. Subtracting it from itself would
    // print "no change over 30 days" about a cluster whose first snapshot
    // landed this morning.
    expect(policyComplianceView(score(), []).history.delta).toBeNull();
    expect(
      policyComplianceView(score(), [{ date: "2026-09-20", score: 90 }]).history
        .delta,
    ).toBeNull();
    expect(
      policyComplianceView(score(), [
        { date: "2026-09-19", score: "nope" },
        { date: "2026-09-20", score: 90 },
      ]).history.delta,
    ).toBeNull();
  });

  test("a history payload of the wrong shape is unavailable, not a throw", () => {
    for (const payload of ["nope", 7, { points: [] }]) {
      const view = policyComplianceView(score(), payload);
      expect(view.history.available).toBe(false);
      expect(view.history.delta).toBeNull();
    }
  });
});

// --------------------------------------------------------------------------
// policy-violations
// --------------------------------------------------------------------------

/** One entry of `GET /v1/policies/violations`, the handler's
 * `NormalizedViolation`. */
function violation(over: Record<string, unknown> = {}): unknown {
  return {
    policy: "require-limits",
    rule: "check-limits",
    severity: "medium",
    action: "audited",
    message: "resource limits are required",
    namespace: "prod",
    kind: "Deployment",
    name: "api",
    engine: "kyverno",
    blocking: false,
    ...over,
  };
}

describe("policyViolationsView", () => {
  test("an installed engine with zero violations is CLEAR, not unavailable", () => {
    // The acceptance example this whole unit exists for. Reaching this
    // function at all means `policies-status` reported an engine present, so
    // an empty list here means the engine has nothing to report -- which is
    // good news and must read as good news. The opposite case, no engine at
    // all, never gets here: the shell resolves it `unavailable` and never
    // calls `render` (KTD1).
    const view = policyViolationsView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.rows).toEqual([]);
    expect(view.bySeverity.every((b) => b.count === 0)).toBe(true);
  });

  test("a null body is an empty list, not an unreadable one", () => {
    // `filterViolationsByRBAC` builds its result with `var filtered []T`, so a
    // user who can see no namespace -- and an engine with nothing to report --
    // both serialise as JSON `null` rather than `[]`. Treating that as
    // unreadable would put "we could not read this" on the healthiest possible
    // cluster.
    const view = policyViolationsView(null, 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
  });

  test("an unreadable payload is unreadable, never an empty list", () => {
    for (const payload of ["nope", 7, { violations: [] }]) {
      const view = policyViolationsView(payload, 5);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("rows rank critical above high above medium above low", () => {
    const view = policyViolationsView(
      [
        violation({ severity: "low", policy: "a" }),
        violation({ severity: "critical", policy: "b" }),
        violation({ severity: "medium", policy: "c" }),
        violation({ severity: "high", policy: "d" }),
      ],
      10,
    );
    expect(view.rows.map((r) => r.severity)).toEqual([
      "critical",
      "high",
      "medium",
      "low",
    ]);
  });

  test("an unrecognised severity ranks last rather than throwing", () => {
    const view = policyViolationsView(
      [
        violation({ severity: "informational", policy: "a" }),
        violation({ severity: "low", policy: "b" }),
        violation({ severity: "critical", policy: "c" }),
      ],
      10,
    );
    expect(view.rows.map((r) => r.severity)).toEqual([
      "critical",
      "low",
      "informational",
    ]);
  });

  test("ties break on policy then namespace then name, so refreshes do not reshuffle", () => {
    const view = policyViolationsView(
      [
        violation({ severity: "high", policy: "p", namespace: "b", name: "z" }),
        violation({ severity: "high", policy: "p", namespace: "a", name: "z" }),
        violation({ severity: "high", policy: "o", namespace: "z", name: "a" }),
        violation({ severity: "high", policy: "p", namespace: "a", name: "y" }),
      ],
      10,
    );
    expect(
      view.rows.map((r) => `${r.policy}/${r.namespace}/${r.name}`),
    ).toEqual(["o/z/a", "p/a/y", "p/a/z", "p/b/z"]);
  });

  test("the counts cover every violation, the rows only the first page of them", () => {
    const many = Array.from({ length: 9 }, (_, i) =>
      violation({ severity: i < 4 ? "critical" : "low", name: `w${i}` }),
    );
    const view = policyViolationsView(many, 3);
    expect(view.total).toBe(9);
    expect(view.rows).toHaveLength(3);
    expect(view.bySeverity).toEqual([
      { severity: "critical", count: 4 },
      { severity: "high", count: 0 },
      { severity: "medium", count: 0 },
      { severity: "low", count: 5 },
    ]);
  });

  test("an entry with no readable identity is counted apart, not rendered blank", () => {
    // A row with no kind and no name is a line of empty space with a severity
    // chip on it. Reporting how many were dropped says more than rendering
    // them would.
    const view = policyViolationsView(
      [violation(), null, "nope", { severity: "high" }],
      10,
    );
    expect(view.total).toBe(4);
    expect(view.rows).toHaveLength(1);
    expect(view.unreadableRows).toBe(3);
  });

  test("blocking is carried through so the card can separate enforced from audited", () => {
    const view = policyViolationsView(
      [violation({ blocking: true }), violation({ blocking: false })],
      10,
    );
    expect(view.rows.map((r) => r.blocking)).toEqual([true, false]);
    expect(view.blocking).toBe(1);
  });
});

// --------------------------------------------------------------------------
// vulnerability-severity
// --------------------------------------------------------------------------

/** One entry of the `vulnerabilities` array in `GET
 * /v1/scanning/vulnerabilities?namespace=`, the handler's
 * `WorkloadVulnSummary`. */
function workload(over: {
  name: string;
  kind?: string;
  namespace?: string;
  critical?: number;
  high?: number;
  medium?: number;
  low?: number;
  scanner?: string;
}): unknown {
  return {
    namespace: over.namespace ?? "prod",
    kind: over.kind ?? "Deployment",
    name: over.name,
    images: [],
    total: {
      critical: over.critical ?? 0,
      high: over.high ?? 0,
      medium: over.medium ?? 0,
      low: over.low ?? 0,
    },
    lastScanned: "2026-09-20T09:00:00Z",
    scanner: over.scanner ?? "trivy",
  };
}

describe("vulnerabilitySeverityView", () => {
  test("a scanner with no findings is CLEAR, not unavailable", () => {
    // The scanning half of the acceptance example. Reaching here means
    // `scanning-status` reported Trivy or Kubescape present; an empty
    // vulnerabilities array from an installed scanner means the namespace is
    // clean, and a card that read it as "no scanner" would hide the good news
    // behind a shrug.
    const view = vulnerabilitySeverityView(
      { vulnerabilities: [], summary: { total: 0, severity: {} } },
      5,
    );
    expect(view.readable).toBe(true);
    expect(view.scanned).toBe(0);
    expect(view.findings).toBe(0);
    expect(view.rows).toEqual([]);
  });

  test("a null vulnerabilities array is an empty list, not an unreadable one", () => {
    const view = vulnerabilitySeverityView(
      { vulnerabilities: null, summary: null },
      5,
    );
    expect(view.readable).toBe(true);
    expect(view.scanned).toBe(0);
    expect(view.findings).toBe(0);
  });

  test("an unreadable payload is unreadable, never a clean namespace", () => {
    for (const payload of [null, undefined, "nope", 7, []]) {
      const view = vulnerabilitySeverityView(payload, 5);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("the breakdown prefers the server's own summary over re-adding the page", () => {
    // U7's convention: a status the server already computed is not re-derived
    // in the browser. The summary counts every workload the handler returned;
    // re-summing the array would agree today and drift the moment the route
    // starts paginating.
    const view = vulnerabilitySeverityView(
      {
        vulnerabilities: [workload({ name: "api", critical: 1 })],
        summary: {
          total: 12,
          severity: { critical: 9, high: 4, medium: 2, low: 1 },
        },
      },
      5,
    );
    expect(view.bySeverity).toEqual([
      { severity: "critical", count: 9 },
      { severity: "high", count: 4 },
      { severity: "medium", count: 2 },
      { severity: "low", count: 1 },
    ]);
    expect(view.findings).toBe(16);
    expect(view.scanned).toBe(1);
  });

  test("a missing summary falls back to the workloads actually returned", () => {
    const view = vulnerabilitySeverityView(
      {
        vulnerabilities: [
          workload({ name: "api", critical: 2, low: 1 }),
          workload({ name: "web", high: 3 }),
        ],
      },
      5,
    );
    expect(view.bySeverity).toEqual([
      { severity: "critical", count: 2 },
      { severity: "high", count: 3 },
      { severity: "medium", count: 0 },
      { severity: "low", count: 1 },
    ]);
    expect(view.findings).toBe(6);
  });

  test("workloads rank by their worst severity first", () => {
    const view = vulnerabilitySeverityView(
      {
        vulnerabilities: [
          workload({ name: "low-only", low: 900 }),
          workload({ name: "one-critical", critical: 1 }),
          workload({ name: "many-high", high: 40 }),
        ],
      },
      5,
    );
    // Nine hundred low findings are not a reason to patch anything before the
    // single critical one. Ranking on the total would invert that.
    expect(view.rows.map((r) => r.name)).toEqual([
      "one-critical",
      "many-high",
      "low-only",
    ]);
  });

  test("workloads with no findings are not ranked", () => {
    // A scanned, clean workload on a "what to patch first" list asserts it has
    // something to patch.
    const view = vulnerabilitySeverityView(
      {
        vulnerabilities: [
          workload({ name: "clean" }),
          workload({ name: "dirty", medium: 1 }),
        ],
      },
      5,
    );
    expect(view.scanned).toBe(2);
    expect(view.rows.map((r) => r.name)).toEqual(["dirty"]);
  });

  test("the rows are capped and the cap is visible in the counts", () => {
    const many = Array.from({ length: 8 }, (_, i) =>
      workload({ name: `w${i}`, critical: 8 - i }),
    );
    const view = vulnerabilitySeverityView({ vulnerabilities: many }, 3);
    expect(view.scanned).toBe(8);
    expect(view.affected).toBe(8);
    expect(view.rows).toHaveLength(3);
  });

  test("an entry with no readable identity is counted apart, not rendered blank", () => {
    const view = vulnerabilitySeverityView(
      { vulnerabilities: [workload({ name: "api", high: 1 }), null, "nope"] },
      5,
    );
    expect(view.rows).toHaveLength(1);
    expect(view.unreadableRows).toBe(2);
  });

  test("a non-finite count is not a finding", () => {
    const view = vulnerabilitySeverityView(
      {
        vulnerabilities: [
          {
            namespace: "prod",
            kind: "Deployment",
            name: "api",
            total: { critical: "many", high: null, medium: 2, low: undefined },
          },
        ],
      },
      5,
    );
    expect(view.findings).toBe(2);
    expect(view.rows[0].critical).toBe(0);
  });
});

// --------------------------------------------------------------------------
// Full-page links (R7)
// --------------------------------------------------------------------------

describe("page links", () => {
  test("each card links to the page that owns its subject", () => {
    expect(COMPLIANCE_PAGE_HREF).toBe("/security/compliance");
    expect(VIOLATIONS_PAGE_HREF).toBe("/security/violations");
    expect(VULNERABILITIES_PAGE_HREF).toBe("/security/vulnerabilities");
  });

  test("a workload deep link encodes every segment it interpolates", () => {
    expect(workloadHref("pro d", "Deployment", "a/b")).toBe(
      "/security/vulnerabilities/pro%20d/Deployment/a%2Fb",
    );
  });
});
