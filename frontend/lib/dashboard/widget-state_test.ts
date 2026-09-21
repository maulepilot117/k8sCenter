import { describe, expect, test } from "bun:test";
import type { SourceState } from "./data.ts";
import { sourceKeyFor } from "./params.ts";
import type { DataSourceKey, FamilyStatusKey } from "./types.ts";
import {
  featurePresent,
  resolveWidgetState,
  sourcesOf,
  type WidgetSourceDecl,
} from "./widget-state.ts";

// Which of the five outcomes a widget box shows. WidgetHost is a component and
// therefore untestable in this repo (D-10), so the decision it makes lives
// here: it is the difference between an operator reading "cert-manager is not
// installed" and reading an empty, healthy-looking card about certificates
// that do not exist. The shell only renders what this returns.

function src(over: Partial<SourceState> = {}): SourceState {
  return {
    data: null,
    error: null,
    errorKind: null,
    loading: false,
    range: null,
    ...over,
  };
}

/** A source that resolved with data. `{}` stands for any payload. */
const ok = (data: unknown = {}) => src({ data });
/** A source that is in flight and has never landed. */
const loading = () => src({ loading: true });
/** A first-fetch failure: no data to fall back on. */
const failed = (message = "boom") =>
  src({ error: message, errorKind: "failure" });
const forbidden = (message = "Forbidden") =>
  src({ error: message, errorKind: "permission" });
/**
 * A read the deployment does not serve: the route answered 503 (or, for the
 * notification centre, the 404 of a route that is not registered without a
 * database) and `ABSENT_STATUSES` in types.ts classified it as absence rather
 * than failure.
 */
const absent = (message = "cluster management requires a database") =>
  src({ error: message, errorKind: "absent" });

/**
 * A read the feature is present for and cannot answer as asked: the route
 * answered a status `UNSUPPORTED_STATUSES` in types.ts names, which today is
 * the 400 `mesh-golden-signals` returns on a cluster running two meshes.
 */
const unsupported = (message = "specify ?mesh= on a dual-mesh cluster") =>
  src({ error: message, errorKind: "unsupported" });

/** Builds the lookup `resolveWidgetState` reads, defaulting anything the test
 * did not name to idle -- which is what an unrequested key actually reads as. */
function lookup(
  states: Partial<Record<DataSourceKey, SourceState>>,
): (key: string) => SourceState {
  // Keyed by string, not by DataSourceKey: the lookup `resolveWidgetState`
  // reads takes a RESOLVED key, which for a parameterized source is the
  // source name plus its values and is therefore not in the union.
  return (key) => states[key as DataSourceKey] ?? src();
}

function decl(over: Partial<WidgetSourceDecl> = {}): WidgetSourceDecl {
  return { sources: ["dashboard-summary"], ...over };
}

describe("featurePresent", () => {
  // Eight families, three payload shapes. The boolean families (cert-manager,
  // ESO, Velero, snapshots) say `detected: false`; the string families (policy,
  // GitOps, mesh, scanning) say `detected: ""` and otherwise name which
  // implementation was found. One rule covers all eight, which is why every
  // widget can declare a family status without the shell learning eight
  // payload shapes.
  test("a boolean family reports absence as detected:false", () => {
    expect(featurePresent({ detected: false })).toBe(false);
    expect(featurePresent({ detected: true })).toBe(true);
  });

  test("a string family reports absence as the empty string", () => {
    expect(featurePresent({ detected: "" })).toBe(false);
    expect(featurePresent({ detected: "istio" })).toBe(true);
    expect(featurePresent({ detected: "both" })).toBe(true);
  });

  test("a payload with no detected field counts as absent", () => {
    // Conservative on purpose: the Definition of Done forbids rendering an
    // absent feature as a healthy one, so an unreadable payload says
    // "not installed" rather than falling through to a green card.
    expect(featurePresent({})).toBe(false);
    expect(featurePresent(null)).toBe(false);
    expect(featurePresent("nonsense")).toBe(false);
    expect(featurePresent({ detected: null })).toBe(false);
  });
});

describe("sourcesOf", () => {
  test("a widget with no family status reads exactly what it declares", () => {
    expect(sourcesOf(decl({ sources: ["dashboard-summary"] }))).toEqual([
      "dashboard-summary",
    ]);
  });

  test("a declared family status is fetched alongside the widget's own", () => {
    expect(
      sourcesOf(
        decl({
          sources: ["dashboard-summary"],
          familyStatus: "certificates-status",
        }),
      ),
    ).toEqual(["dashboard-summary", "certificates-status"]);
  });

  test("a family status also named in sources is not requested twice", () => {
    expect(
      sourcesOf(
        decl({
          sources: ["dashboard-summary", "certificates-status"],
          familyStatus: "certificates-status",
        }),
      ),
    ).toEqual(["dashboard-summary", "certificates-status"]);
  });
});

describe("resolveWidgetState -- a widget declaring no family status", () => {
  // Every widget shipped today is in this group, so this describe block is
  // the "this unit is invisible" proof: each case must match what WidgetHost
  // did before availability existed.
  test("all required sources resolved renders", () => {
    const r = resolveWidgetState(decl(), lookup({ "dashboard-summary": ok() }));
    expect(r.state).toBe("ready");
    expect(r.stale).toBeNull();
  });

  test("a required source in flight is the skeleton", () => {
    expect(
      resolveWidgetState(decl(), lookup({ "dashboard-summary": loading() }))
        .state,
    ).toBe("loading");
  });

  test("an unrequested source is the skeleton, not an error", () => {
    expect(resolveWidgetState(decl(), lookup({})).state).toBe("loading");
  });

  test("a required first-fetch failure is the error state", () => {
    const r = resolveWidgetState(
      decl(),
      lookup({ "dashboard-summary": failed("500") }),
    );
    expect(r.state).toBe("error");
    expect(r.blocking?.error).toBe("500");
  });

  test("an optional source that failed does not block the widget", () => {
    const r = resolveWidgetState(
      decl({
        sources: ["dashboard-summary", "dashboard-trends"],
        optionalSources: ["dashboard-trends"],
      }),
      lookup({
        "dashboard-summary": ok(),
        "dashboard-trends": failed(),
      }),
    );
    expect(r.state).toBe("ready");
    // Nothing stale to show: the trend source failed before it ever landed.
    expect(r.stale).toBeNull();
  });

  test("stale data plus a new error still renders, with the stale notice", () => {
    const r = resolveWidgetState(
      decl(),
      lookup({
        "dashboard-summary": src({
          data: { pods: 3 },
          error: "502",
          errorKind: "failure",
        }),
      }),
    );
    expect(r.state).toBe("ready");
    expect(r.stale?.error).toBe("502");
  });

  test("a required source in flight beside a failed one shows the failure", () => {
    const r = resolveWidgetState(
      decl({ sources: ["dashboard-summary", "cluster-info"] }),
      lookup({
        "dashboard-summary": failed("500"),
        "cluster-info": loading(),
      }),
    );
    expect(r.state).toBe("error");
  });
});

describe("resolveWidgetState -- permission", () => {
  test("a required source refused with 403 is the permission state", () => {
    const r = resolveWidgetState(
      decl(),
      lookup({ "dashboard-summary": forbidden("Forbidden") }),
    );
    expect(r.state).toBe("permission");
    expect(r.blocking?.error).toBe("Forbidden");
  });

  test("a refused family status is the permission state, not unavailable", () => {
    // Not knowing whether cert-manager is installed is a different answer from
    // knowing it is not, and the account is the reason for the first.
    const r = resolveWidgetState(
      decl({ familyStatus: "certificates-status" }),
      lookup({
        "dashboard-summary": ok(),
        "certificates-status": forbidden(),
      }),
    );
    expect(r.state).toBe("permission");
  });

  test("permission outranks a plain failure when both required sources fail", () => {
    // A retry affordance on a 403 is a lie, so the state that carries none
    // has to win when the two land together (R2).
    const r = resolveWidgetState(
      decl({ sources: ["dashboard-summary", "cluster-info"] }),
      lookup({
        "dashboard-summary": failed("500"),
        "cluster-info": forbidden(),
      }),
    );
    expect(r.state).toBe("permission");
  });

  test("an optional source refused with 403 does not block the widget", () => {
    const r = resolveWidgetState(
      decl({
        sources: ["dashboard-summary", "dashboard-trends"],
        optionalSources: ["dashboard-trends"],
      }),
      lookup({
        "dashboard-summary": ok(),
        "dashboard-trends": forbidden(),
      }),
    );
    expect(r.state).toBe("ready");
  });
});

describe("resolveWidgetState -- a source the deployment does not serve", () => {
  // The platform family's routes are not CRD-discovered, so none of them has a
  // discovery status to declare. What can be missing is the DEPLOYMENT's
  // database, and the four routes that need one say so with a 503 -- a
  // service-unavailable error, which without this would land on the generic
  // error card: "could not be loaded", with a retry that will never work, for
  // a deployment that is configured exactly as its operator meant it to be.

  test("a required source classified absent is unavailable, not an error", () => {
    const r = resolveWidgetState(
      decl(),
      lookup({ "dashboard-summary": absent() }),
    );
    expect(r.state).toBe("unavailable");
  });

  test("the unavailable state carries no blocking source", () => {
    // The shell renders no message for it, exactly as it renders none for a
    // family that reports its feature absent. Handing it one would invite a
    // card that prints "503 Service Unavailable" at an operator who has simply
    // not configured a database.
    const r = resolveWidgetState(
      decl(),
      lookup({ "dashboard-summary": absent() }),
    );
    expect(r.blocking).toBeNull();
    expect(r.stale).toBeNull();
  });

  test("absence outranks both a refusal and a failure", () => {
    // A feature the deployment does not run cannot be permitted, and cannot
    // usefully be retried. Ranking a 403 above it would tell an operator their
    // account is the problem when the deployment is.
    const r = resolveWidgetState(
      decl({ sources: ["dashboard-summary", "cluster-info", "recent-events"] }),
      lookup({
        "dashboard-summary": failed("500"),
        "cluster-info": forbidden(),
        "recent-events": absent(),
      }),
    );
    expect(r.state).toBe("unavailable");
  });

  test("an OPTIONAL source the deployment does not serve does not withhold the widget", () => {
    // Same rule every other outcome follows: a source the widget declared
    // optional never gates it. A card blanked by a secondary read the
    // deployment does not serve is the failure `optionalSources` exists for.
    const r = resolveWidgetState(
      decl({
        sources: ["dashboard-summary", "dashboard-trends"],
        optionalSources: ["dashboard-trends"],
      }),
      lookup({ "dashboard-summary": ok(), "dashboard-trends": absent() }),
    );
    expect(r.state).toBe("ready");
  });

  test("data already on screen outranks a source that has gone absent", () => {
    // The ready branch is checked first for everything else and is checked
    // first for this too: a route that starts answering 503 mid-session leaves
    // the last good reading on screen under the stale notice rather than
    // replacing a working card with "not installed".
    const r = resolveWidgetState(
      decl(),
      lookup({
        "dashboard-summary": src({
          data: { a: 1 },
          error: "gone",
          errorKind: "absent",
        }),
      }),
    );
    expect(r.state).toBe("ready");
    expect(r.stale?.error).toBe("gone");
  });
});

describe("resolveWidgetState -- availability", () => {
  test("a family that reports its feature absent is unavailable", () => {
    const r = resolveWidgetState(
      decl({ familyStatus: "certificates-status" }),
      lookup({
        "dashboard-summary": ok(),
        "certificates-status": ok({ detected: false }),
      }),
    );
    expect(r.state).toBe("unavailable");
  });

  test("a present feature with an empty list renders the widget's own empty copy", () => {
    // R1's whole point: an empty list is not evidence of absence. The widget
    // is rendered and says "no certificates" in its own words.
    const r = resolveWidgetState(
      decl({ familyStatus: "certificates-status" }),
      lookup({
        "dashboard-summary": ok({ items: [] }),
        "certificates-status": ok({ detected: true }),
      }),
    );
    expect(r.state).toBe("ready");
  });

  test("a string family naming its implementation is present", () => {
    const r = resolveWidgetState(
      decl({ familyStatus: "mesh-status" }),
      lookup({
        "dashboard-summary": ok(),
        "mesh-status": ok({ detected: "linkerd" }),
      }),
    );
    expect(r.state).toBe("ready");
  });

  test("a family status still in flight is the skeleton, not unavailable", () => {
    // Guessing "not installed" from a status that has not answered yet would
    // flash the wrong verdict on every mount.
    const r = resolveWidgetState(
      decl({ familyStatus: "mesh-status" }),
      lookup({
        "dashboard-summary": ok(),
        "mesh-status": loading(),
      }),
    );
    expect(r.state).toBe("loading");
  });

  test("a failed family status is the error state, not unavailable", () => {
    const r = resolveWidgetState(
      decl({ familyStatus: "mesh-status" }),
      lookup({
        "dashboard-summary": ok(),
        "mesh-status": failed("504"),
      }),
    );
    expect(r.state).toBe("error");
  });

  test("absence is reported as soon as the family status says so", () => {
    // This test used to assert the opposite, on the premise that "an absent
    // feature's own endpoints still answer (200, empty)" so the widget would
    // reach unavailable a moment later anyway. That premise is false for two
    // widgets in this catalog: the Hubble flows route answers 503 when Hubble
    // is absent and the mesh golden-signals route answers 400 when no mesh is
    // detected. On those clusters the moment never came -- the required
    // source errored and the widget fell through to the error card, which is
    // the one outcome the unavailable state exists to prevent.
    //
    // Once the status has landed and reports the feature absent, no other
    // source can change the answer. There is nothing to wait for.
    const r = resolveWidgetState(
      decl({ familyStatus: "mesh-status" }),
      lookup({
        "dashboard-summary": loading(),
        "mesh-status": ok({ detected: "" }),
      }),
    );
    expect(r.state).toBe("unavailable");
  });

  test("an absent feature outranks its own route's failure", () => {
    // The case the reordering above exists for: a feature whose data route
    // refuses rather than answering empty. Reported as not installed, not as
    // a failure with a retry that cannot succeed.
    const r = resolveWidgetState(
      decl({ familyStatus: "mesh-status" }),
      lookup({
        "dashboard-summary": failed("400 no service mesh detected"),
        "mesh-status": ok({ detected: "" }),
      }),
    );
    expect(r.state).toBe("unavailable");
    expect(r.blocking).toBeNull();
  });

  test("a family status that has not landed still waits", () => {
    // The reordering does not make absence win a race against its own
    // evidence: with no verdict yet, there is nothing to report.
    const r = resolveWidgetState(
      decl({ familyStatus: "mesh-status" }),
      lookup({
        "dashboard-summary": ok(),
        "mesh-status": loading(),
      }),
    );
    expect(r.state).toBe("loading");
  });

  test("an absent feature outranks a stale-data notice", () => {
    // Nothing is rendered, so there is no card for the notice to sit above.
    const r = resolveWidgetState(
      decl({ familyStatus: "mesh-status" }),
      lookup({
        "dashboard-summary": src({
          data: { items: [] },
          error: "502",
          errorKind: "failure",
        }),
        "mesh-status": ok({ detected: "" }),
      }),
    );
    expect(r.state).toBe("unavailable");
    expect(r.stale).toBeNull();
  });
});

describe("every family status key is declarable", () => {
  test("each key resolves to unavailable when its payload says absent", () => {
    const keys: FamilyStatusKey[] = [
      "policies-status",
      "gitops-status",
      "certificates-status",
      "mesh-status",
      "external-secrets-status",
      "velero-status",
      "scanning-status",
    ];
    const offenders = keys.filter(
      (key) =>
        resolveWidgetState(
          decl({ familyStatus: key }),
          lookup({
            "dashboard-summary": ok(),
            [key]: ok({ detected: false }),
          }),
        ).state !== "unavailable",
    );
    expect(offenders).toEqual([]);
  });
});

describe("resolveWidgetState: parameterized sources", () => {
  // A parameterized widget reads its source under a key that carries the
  // values, so the state it resolves against has to be looked up under the
  // same key. Resolving against the bare source key instead is the defect
  // this covers, and it is a quiet one: the bare key is never fetched, so it
  // reads as idle and the widget sits in the skeleton forever.
  const paramDecl = decl({ sources: ["diagnostics-summary"] });
  const prod = sourceKeyFor("diagnostics-summary", { namespace: "prod" });
  const staging = sourceKeyFor("diagnostics-summary", {
    namespace: "staging",
  });

  test("resolves against the key its parameters produce", () => {
    const stateOf = (key: string) =>
      key === prod ? ok({ total: 1, failing: [] }) : src();
    expect(
      resolveWidgetState(paramDecl, stateOf, { namespace: "prod" }).state,
    ).toBe("ready");
  });

  test("two namespaces resolve independently", () => {
    // The whole reason the key carries the values: a 403 on one namespace
    // must not put the other namespace's card into the permission state.
    const stateOf = (key: string) => {
      if (key === prod) return ok({ total: 1, failing: [] });
      if (key === staging) return forbidden();
      return src();
    };
    expect(
      resolveWidgetState(paramDecl, stateOf, { namespace: "prod" }).state,
    ).toBe("ready");
    expect(
      resolveWidgetState(paramDecl, stateOf, { namespace: "staging" }).state,
    ).toBe("permission");
  });

  test("a refused read is the permission state, with no retry offered", () => {
    // R5's client half. The server withholds a placement whose namespace the
    // caller may not read on LOAD; access lost while the dashboard is already
    // open surfaces as a 403 on the next refresh instead, and lands here.
    const resolved = resolveWidgetState(
      paramDecl,
      (key) => (key === prod ? forbidden() : src()),
      { namespace: "prod" },
    );
    expect(resolved.state).toBe("permission");
    expect(resolved.blocking?.errorKind).toBe("permission");
  });

  test("a widget with no parameters is unaffected", () => {
    // The path every shipped widget took before parameters existed: an
    // unparameterized source keeps its bare key however it is called.
    expect(
      resolveWidgetState(decl(), lookup({ "dashboard-summary": ok() }), {})
        .state,
    ).toBe("ready");
  });
});

describe("unsupported", () => {
  // The dual-mesh case. The mesh family status reports the mesh PRESENT --
  // two are -- so the unavailable branch cannot fire, and before this state
  // existed the 400 fell through to the generic error card: warning colour
  // and a retry that could never succeed, on a cluster where nothing is
  // broken and nothing will change by asking again.
  test("a present feature that cannot answer is not unavailable and not an error", () => {
    const r = resolveWidgetState(
      decl({ sources: ["mesh-golden-signals"], familyStatus: "mesh-status" }),
      lookup({
        "mesh-status": src({ data: { detected: "istio" } }),
        "mesh-golden-signals": unsupported(),
      }),
    );
    expect(r.state).toBe("unsupported");
  });

  // The whole point of the state: the heading cannot explain this one on its
  // own, so the message has to reach the card.
  test("the blocking source rides along, because its message is the explanation", () => {
    const r = resolveWidgetState(
      decl({ sources: ["mesh-golden-signals"], familyStatus: "mesh-status" }),
      lookup({
        "mesh-status": src({ data: { detected: "istio" } }),
        "mesh-golden-signals": unsupported("cluster runs istio and linkerd"),
      }),
    );
    expect(r.blocking?.error).toBe("cluster runs istio and linkerd");
  });

  // Absence is the more specific claim and keeps precedence: a cluster that
  // does not run the mesh at all must read as not-installed, not as a request
  // that needs refining.
  test("a feature reported absent still outranks it", () => {
    const r = resolveWidgetState(
      decl({ sources: ["mesh-golden-signals"], familyStatus: "mesh-status" }),
      lookup({
        "mesh-status": src({ data: { detected: "" } }),
        "mesh-golden-signals": unsupported(),
      }),
    );
    expect(r.state).toBe("unavailable");
  });
});
