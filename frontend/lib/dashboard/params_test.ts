import { describe, expect, test } from "bun:test";
import {
  canonicalParams,
  decodeSourceKey,
  duplicatePlacementOf,
  MAX_PARAM_VALUE_LEN,
  missingParamKeys,
  PARAM_KEY_NAMESPACE,
  PARAMETERIZED_SOURCE_PARAMS,
  paramValueError,
  sourceKeyFor,
  widgetSourceKeys,
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
    expect(paramValueError("kube-system", [])).toBeNull();
  });

  test("an empty value is refused", () => {
    // The dialog places nothing until every declared key has a value; without
    // this the user could confirm an empty namespace and get a widget that
    // reads /v1/diagnostics//summary.
    expect(paramValueError("", [])).not.toBeNull();
  });

  test("a value longer than the server's bound is refused", () => {
    const tooLong = "a".repeat(MAX_PARAM_VALUE_LEN + 1);
    expect(paramValueError(tooLong, [])).not.toBeNull();
    expect(paramValueError("a".repeat(MAX_PARAM_VALUE_LEN), [])).toBeNull();
  });

  test("the bound counts runes, the way the server does", () => {
    // utf8.RuneCountInString, not len(). A value of 253 astral code points is
    // accepted by the server and must be accepted here, and 254 refused.
    const astral = "\u{1F600}".repeat(MAX_PARAM_VALUE_LEN);
    expect(paramValueError(astral, [])).toBeNull();
    expect(paramValueError(astral + "\u{1F600}", [])).not.toBeNull();
  });

  test("a control character is refused", () => {
    // unicode.IsControl on the Go side: C0 and C1, not merely newline.
    expect(paramValueError("pro\nd", [])).not.toBeNull();
    expect(paramValueError("pro\u0000d", [])).not.toBeNull();
    expect(paramValueError("pro\u009fd", [])).not.toBeNull();
  });

  test("a closed value set is closed", () => {
    expect(paramValueError("istio", ["istio", "linkerd"])).toBeNull();
    expect(paramValueError("consul", ["istio", "linkerd"])).not.toBeNull();
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
