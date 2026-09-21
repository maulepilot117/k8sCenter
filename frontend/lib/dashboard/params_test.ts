import { describe, expect, test } from "bun:test";
import {
  canonicalParams,
  decodeSourceKey,
  duplicatePlacementOf,
  KNOWN_PARAM_KEYS,
  MAX_PARAM_VALUE_LEN,
  missingParamKeys,
  PARAM_KEY_NAMESPACE,
  PARAM_KEY_SERVICE,
  PARAMETERIZED_SOURCE_PARAMS,
  paramFieldState,
  paramParentKey,
  paramValueError,
  sourceKeyFor,
  widgetSourceKeys,
  withParamValue,
} from "./params.ts";
import type { LayoutItem } from "./types.ts";
import { DATA_SOURCE_KEYS } from "./types.ts";

// The parameter rules a widget's values have to satisfy before a save is even
// attempted, and the cache key a parameterized read is issued under.
//
// All of it is pure and all of it mirrors something in Go: the bounds mirror
// `ValidateDashboardLayout` in backend/internal/preferences/dashboard.go, and
// the duplicate identity mirrors that validator's `canonicalParams`. The point
// of mirroring is that the dialog can refuse a value the server would refuse
// WITHOUT a round trip -- so every case below is a case where the two have to
// agree, and a drift here is a dialog that accepts something and then loses
// the user's whole layout on save.

function item(
  instanceId: string,
  id: string,
  params?: Record<string, string>,
): LayoutItem {
  return {
    instanceId,
    id,
    x: 0,
    y: 0,
    w: 3,
    h: 3,
    ...(params ? { params } : {}),
  };
}

describe("paramValueError", () => {
  test("an ordinary namespace name is accepted", () => {
    expect(paramValueError(PARAM_KEY_NAMESPACE, "kube-system", [])).toBeNull();
  });

  test("an empty value is refused", () => {
    // The dialog places nothing until every declared key has a value; without
    // this the user could confirm an empty namespace and get a widget that
    // reads /v1/diagnostics//summary.
    expect(paramValueError(PARAM_KEY_NAMESPACE, "", [])).not.toBeNull();
  });

  test("a value longer than the server's bound is refused", () => {
    const tooLong = "a".repeat(MAX_PARAM_VALUE_LEN + 1);
    expect(paramValueError(PARAM_KEY_NAMESPACE, tooLong, [])).not.toBeNull();
    expect(
      paramValueError(PARAM_KEY_NAMESPACE, "a".repeat(MAX_PARAM_VALUE_LEN), []),
    ).toBeNull();
  });

  test("the bound counts runes, the way the server does", () => {
    // utf8.RuneCountInString, not len(). A value of 253 astral code points is
    // accepted by the server and must be accepted here, and 254 refused.
    const astral = "\u{1F600}".repeat(MAX_PARAM_VALUE_LEN);
    expect(paramValueError(PARAM_KEY_NAMESPACE, astral, [])).toBeNull();
    expect(
      paramValueError(PARAM_KEY_NAMESPACE, astral + "\u{1F600}", []),
    ).not.toBeNull();
  });

  test("a control character is refused", () => {
    // unicode.IsControl on the Go side: C0 and C1, not merely newline.
    expect(paramValueError(PARAM_KEY_NAMESPACE, "pro\nd", [])).not.toBeNull();
    expect(
      paramValueError(PARAM_KEY_NAMESPACE, "pro\u0000d", []),
    ).not.toBeNull();
    expect(
      paramValueError(PARAM_KEY_NAMESPACE, "pro\u009fd", []),
    ).not.toBeNull();
  });

  test("a closed value set is closed", () => {
    expect(paramValueError("mesh", "istio", ["istio", "linkerd"])).toBeNull();
    expect(
      paramValueError("mesh", "consul", ["istio", "linkerd"]),
    ).not.toBeNull();
  });
});

describe("missingParamKeys", () => {
  test("a widget that declares nothing is never missing anything", () => {
    expect(missingParamKeys(undefined, {})).toEqual([]);
  });

  test("a declared key with no value is named", () => {
    expect(missingParamKeys({ namespace: [] }, {})).toEqual(["namespace"]);
    expect(missingParamKeys({ namespace: [] }, { namespace: "" })).toEqual([
      "namespace",
    ]);
  });

  test("a declared key with a value is not named", () => {
    expect(missingParamKeys({ namespace: [] }, { namespace: "prod" })).toEqual(
      [],
    );
  });
});

describe("canonicalParams", () => {
  test("order does not change the identity", () => {
    expect(canonicalParams({ a: "1", b: "2" })).toBe(
      canonicalParams({ b: "2", a: "1" }),
    );
  });

  test("an empty map and an absent one fold to the same thing", () => {
    expect(canonicalParams({})).toBe(canonicalParams(undefined));
  });

  test("two different maps never fold to one string", () => {
    // Injective for ANY bytes, which is why the encoding is length-prefixed
    // rather than delimited -- the Go side makes the same argument, and the
    // two identities have to agree or a placement the server calls a duplicate
    // is one the client called distinct.
    expect(canonicalParams({ "a=b": "c" })).not.toBe(
      canonicalParams({ a: "b=c" }),
    );
    expect(canonicalParams({ ab: "c" })).not.toBe(canonicalParams({ a: "bc" }));
  });
});

describe("duplicatePlacementOf", () => {
  const placed = [
    item("d-1", "diagnostics-summary", { namespace: "prod" }),
    item("d-2", "diagnostics-summary", { namespace: "staging" }),
    item("n-1", "nodes"),
  ];

  test("the same widget with different params is not a duplicate", () => {
    // The whole reason instanceId exists: diagnostics for prod beside
    // diagnostics for staging.
    expect(
      duplicatePlacementOf(placed, "diagnostics-summary", { namespace: "dev" }),
    ).toBeNull();
  });

  test("the same widget with the same params is a duplicate", () => {
    expect(
      duplicatePlacementOf(placed, "diagnostics-summary", {
        namespace: "prod",
      })?.instanceId,
    ).toBe("d-1");
  });

  test("the placement being re-parameterized is not its own duplicate", () => {
    // Re-opening the dialog on d-1 and pressing Save without changing the
    // namespace must not report a collision with d-1 itself.
    expect(
      duplicatePlacementOf(
        placed,
        "diagnostics-summary",
        { namespace: "prod" },
        "d-1",
      ),
    ).toBeNull();
    // ...but moving d-1 onto d-2's namespace still is one.
    expect(
      duplicatePlacementOf(
        placed,
        "diagnostics-summary",
        { namespace: "staging" },
        "d-1",
      )?.instanceId,
    ).toBe("d-2");
  });

  test("a second copy of an unparameterized widget is a duplicate", () => {
    expect(duplicatePlacementOf(placed, "nodes", {})?.instanceId).toBe("n-1");
  });
});

describe("sourceKeyFor / decodeSourceKey", () => {
  test("a source that takes no parameters keeps its bare key", () => {
    // Otherwise a parameterized widget's cluster-info read would be issued
    // once per namespace, which is the opposite of what the cache is for.
    expect(sourceKeyFor("cluster-info", { namespace: "prod" })).toBe(
      "cluster-info",
    );
  });

  test("a parameterized source carries its values in the key", () => {
    const a = sourceKeyFor("diagnostics-summary", { namespace: "prod" });
    const b = sourceKeyFor("diagnostics-summary", { namespace: "staging" });
    expect(a).not.toBe(b);
    expect(a).not.toBe("diagnostics-summary");
  });

  test("only the parameters the source declares reach its key", () => {
    // A widget may carry parameters a given one of its sources does not read;
    // folding them in would split one cache entry into several.
    expect(
      sourceKeyFor("diagnostics-summary", {
        namespace: "prod",
        unrelated: "x",
      }),
    ).toBe(sourceKeyFor("diagnostics-summary", { namespace: "prod" }));
  });

  test("the encoding round-trips", () => {
    const key = sourceKeyFor("diagnostics-summary", { namespace: "prod" });
    expect(decodeSourceKey(key)).toEqual({
      base: "diagnostics-summary",
      params: { namespace: "prod" },
    });
  });

  test("a bare key decodes to itself with no parameters", () => {
    expect(decodeSourceKey("cluster-info")).toEqual({
      base: "cluster-info",
      params: {},
    });
  });

  test("the encoding is injective for values that contain its own syntax", () => {
    // Length-prefixed rather than delimited, for the reason the Go
    // `canonicalParams` gives: a delimiter only works while no value can
    // contain it, and that precondition lives in another function.
    const a = sourceKeyFor("diagnostics-summary", { namespace: "4:prod" });
    const b = sourceKeyFor("diagnostics-summary", { namespace: "prod" });
    expect(a).not.toBe(b);
    expect(decodeSourceKey(a).params.namespace).toBe("4:prod");
  });

  test("every parameterized source is a declared source key", () => {
    // Mechanical: a table naming a source that does not exist would silently
    // never apply.
    const unknown = Object.keys(PARAMETERIZED_SOURCE_PARAMS).filter(
      (k) => !(DATA_SOURCE_KEYS as readonly string[]).includes(k),
    );
    expect(unknown).toEqual([]);
  });

  test("the namespace parameter is spelled exactly as the server expects", () => {
    // The server special-cases this key and re-authorizes its value on every
    // read (R5). A widget spelling it differently silently opts out of that,
    // and nothing else would notice.
    expect(PARAM_KEY_NAMESPACE).toBe("namespace");
    expect(PARAMETERIZED_SOURCE_PARAMS["diagnostics-summary"]).toEqual([
      PARAM_KEY_NAMESPACE,
    ]);
  });
});

describe("widgetSourceKeys", () => {
  test("a widget's parameterized and unparameterized sources are keyed apart", () => {
    const keys = widgetSourceKeys(
      ["dashboard-summary", "diagnostics-summary"],
      { namespace: "prod" },
    );
    expect(keys).toContain("dashboard-summary");
    expect(keys).toContain(
      sourceKeyFor("diagnostics-summary", { namespace: "prod" }),
    );
  });

  test("two widgets on different namespaces share nothing but the shared key", () => {
    const prod = widgetSourceKeys(
      ["dashboard-summary", "diagnostics-summary"],
      { namespace: "prod" },
    );
    const staging = widgetSourceKeys(
      ["dashboard-summary", "diagnostics-summary"],
      { namespace: "staging" },
    );
    const shared = prod.filter((k) => staging.includes(k));
    expect(shared).toEqual(["dashboard-summary"]);
  });
});

describe("the service key's value shape", () => {
  // The namespace key and the service key are open-valued for the same
  // reason -- neither value space is enumerable from a catalog -- and they
  // are NOT bounded the same way, which is the point of this block.
  //
  // A stored namespace is re-authorized against the live cluster on every
  // read (R5), so a nonsense namespace is inert: it can never authorize, and
  // the placement is withheld. Nothing re-authorizes a service name. Its only
  // defence is its shape, so the shape is the check -- which is D-8 made
  // structural for the one key that would otherwise carry unvalidated caller
  // text into a stored layout.

  test("the service parameter is spelled exactly as the server expects", () => {
    expect(PARAM_KEY_SERVICE).toBe("service");
    expect(KNOWN_PARAM_KEYS).toEqual([PARAM_KEY_NAMESPACE, PARAM_KEY_SERVICE]);
  });

  test("an ordinary service name is accepted", () => {
    expect(paramValueError(PARAM_KEY_SERVICE, "checkout", [])).toBeNull();
    expect(
      paramValueError(PARAM_KEY_SERVICE, "checkout-api-v2", []),
    ).toBeNull();
  });

  test("anything that is not a Kubernetes service name is refused", () => {
    // The shapes D-8 exists to keep out of a stored parameter, and the reason
    // the input is a select over a fetched list rather than a text box: none
    // of these can be produced by choosing from that list, and all of them
    // can be produced by a client that skips it.
    const offenders = [
      "my service", // a space
      "prod/checkout", // a path separator: a different route entirely
      "http://evil.example/x", // a URL
      "sum(rate(x[5m])) or on() vector(0)", // PromQL
      "Checkout", // uppercase: not a DNS label
      "checkout-", // a trailing dash
      "-checkout", // a leading dash
      "9checkout", // a leading digit: legal for a namespace, not a service
      "a".repeat(64), // past the 63-character label bound
    ];
    const accepted = offenders.filter(
      (v) => paramValueError(PARAM_KEY_SERVICE, v, []) === null,
    );
    expect(accepted).toEqual([]);
  });

  test("the longest legal service name is still accepted", () => {
    expect(paramValueError(PARAM_KEY_SERVICE, "a".repeat(63), [])).toBeNull();
  });

  test("the namespace key keeps the generic bounds it shipped with", () => {
    // Deliberately NOT tightened alongside the service key. A namespace value
    // is re-authorized per read, and narrowing it here would refuse a save of
    // a layout the server has been accepting since P3 -- a client that starts
    // rejecting stored values is the drift this module exists to prevent.
    expect(paramValueError(PARAM_KEY_NAMESPACE, "9prod", [])).toBeNull();
    expect(paramValueError(PARAM_KEY_NAMESPACE, "a".repeat(64), [])).toBeNull();
  });
});

describe("the cascading parameter case", () => {
  // A service is only meaningful inside a namespace, and the service list the
  // dialog offers is drawn from the namespace the user picked. That makes the
  // second field DEPENDENT: unusable before the first is answered, and stale
  // the moment the first changes. Both rules live here rather than in the
  // dialog because the dialog is untestable in this repo (D-10).

  test("the service key depends on the namespace key, and nothing depends on the namespace", () => {
    expect(paramParentKey(PARAM_KEY_SERVICE)).toBe(PARAM_KEY_NAMESPACE);
    expect(paramParentKey(PARAM_KEY_NAMESPACE)).toBeNull();
    expect(paramParentKey("mode")).toBeNull();
  });

  test("a dependent field is blocked until its parent has a value", () => {
    expect(paramFieldState(PARAM_KEY_SERVICE, {}, ["checkout"])).toEqual({
      enabled: false,
      blocked: "awaiting-parent",
    });
    expect(
      paramFieldState(PARAM_KEY_SERVICE, { namespace: "" }, ["checkout"]),
    ).toEqual({ enabled: false, blocked: "awaiting-parent" });
  });

  test("a field with a parent but no options is disabled rather than opened up", () => {
    // The namespace the user picked is one they cannot list services in, or
    // one with no services at all. Either way there is nothing to choose, and
    // the answer is an empty disabled select -- never a text box, which is
    // what "fall back to free text" would mean and what D-8 forbids.
    expect(
      paramFieldState(PARAM_KEY_SERVICE, { namespace: "prod" }, []),
    ).toEqual({ enabled: false, blocked: "no-options" });
  });

  test("a field with a parent and options is enabled", () => {
    expect(
      paramFieldState(PARAM_KEY_SERVICE, { namespace: "prod" }, ["checkout"]),
    ).toEqual({ enabled: true, blocked: null });
  });

  test("an independent field with options is enabled", () => {
    expect(paramFieldState(PARAM_KEY_NAMESPACE, {}, ["prod"])).toEqual({
      enabled: true,
      blocked: null,
    });
  });

  test("changing the parent clears the dependent value", () => {
    // The kept-value failure this prevents is silent and specific: a widget
    // re-pointed from prod to staging while still carrying prod's service
    // name would read /v1/mesh/golden-signals for a service that does not
    // exist in staging and render zeros -- a card reporting no traffic for a
    // service that is simply not there.
    expect(
      withParamValue(
        { namespace: "prod", service: "checkout" },
        PARAM_KEY_NAMESPACE,
        "staging",
      ),
    ).toEqual({ namespace: "staging", service: "" });
  });

  test("re-choosing the same parent value still clears, because the option list is refetched", () => {
    expect(
      withParamValue(
        { namespace: "prod", service: "checkout" },
        PARAM_KEY_NAMESPACE,
        "prod",
      ),
    ).toEqual({ namespace: "prod", service: "" });
  });

  test("setting the dependent value leaves the parent alone", () => {
    expect(
      withParamValue(
        { namespace: "prod", service: "" },
        PARAM_KEY_SERVICE,
        "checkout",
      ),
    ).toEqual({ namespace: "prod", service: "checkout" });
  });

  test("setting an unrelated key clears nothing", () => {
    expect(withParamValue({ namespace: "prod" }, "mode", "full")).toEqual({
      namespace: "prod",
      mode: "full",
    });
  });
});

describe("a two-key widget's placement rules", () => {
  // The golden-signals shape, through the two functions the dialog gates on.
  // Both were written for the one-key case and neither needed changing; these
  // pin that, because the failure of either at two keys is a placement the
  // server refuses after the user has arranged a dashboard around it.

  test("a namespace with no service is refused, and both keys are named", () => {
    // The backing read needs both -- `/v1/mesh/golden-signals` answers 400
    // with either missing -- so a confirm at this point would place a card
    // that can never load. The dialog turns this list into "Choose a
    // service.", naming the field rather than saying "choose a value" beside
    // two selects.
    const declared = { namespace: [], service: [] };
    expect(
      missingParamKeys(declared, { namespace: "prod", service: "" }),
    ).toEqual(["service"]);
    expect(missingParamKeys(declared, { namespace: "", service: "" })).toEqual([
      "namespace",
      "service",
    ]);
    expect(
      missingParamKeys(declared, { namespace: "prod", service: "checkout" }),
    ).toEqual([]);
  });

  test("two services in one namespace are two placements, not a duplicate", () => {
    // The case that makes the service key part of the identity rather than
    // decoration on the namespace. Both cards are legitimate and the server
    // agrees: its duplicate rule is the same widget with the same params.
    const placed = [
      item("a", "mesh-golden-signals", {
        namespace: "prod",
        service: "checkout",
      }),
    ];
    expect(
      duplicatePlacementOf(placed, "mesh-golden-signals", {
        namespace: "prod",
        service: "cart",
      }),
    ).toBeNull();
    expect(
      duplicatePlacementOf(placed, "mesh-golden-signals", {
        namespace: "prod",
        service: "checkout",
      }),
    ).not.toBeNull();
  });

  test("key order does not make one placement two", () => {
    // `canonicalParams` sorts, and two keys is the first time that can
    // actually differ: one key cannot be out of order with itself.
    expect(canonicalParams({ namespace: "prod", service: "cart" })).toBe(
      canonicalParams({ service: "cart", namespace: "prod" }),
    );
  });
});

describe("the networking sources' parameters", () => {
  test("golden signals is keyed by both of its values", () => {
    // Both, and not only the namespace: two cards on two services in one
    // namespace are two different reads, and keying on the namespace alone
    // would make them one cache entry showing whichever landed last.
    expect(PARAMETERIZED_SOURCE_PARAMS["mesh-golden-signals"]).toEqual([
      PARAM_KEY_NAMESPACE,
      PARAM_KEY_SERVICE,
    ]);
    expect(
      sourceKeyFor("mesh-golden-signals", {
        namespace: "prod",
        service: "checkout",
      }),
    ).not.toBe(
      sourceKeyFor("mesh-golden-signals", {
        namespace: "prod",
        service: "cart",
      }),
    );
  });

  test("the flows source is keyed by its namespace", () => {
    expect(PARAMETERIZED_SOURCE_PARAMS["hubble-flows"]).toEqual([
      PARAM_KEY_NAMESPACE,
    ]);
  });

  test("a two-value key round-trips through the decoder", () => {
    const key = sourceKeyFor("mesh-golden-signals", {
      namespace: "prod",
      service: "checkout",
    });
    expect(decodeSourceKey(key)).toEqual({
      base: "mesh-golden-signals",
      params: { namespace: "prod", service: "checkout" },
    });
  });
});
