import { expect, test } from "bun:test";
import type { K8sEvent } from "@/lib/k8s-types.ts";
import { asEventList, formatEventLabel, KIND_ABBR } from "./event-format.ts";

function evt(kind?: string, name?: string): K8sEvent {
  return {
    involvedObject: kind || name ? { kind, name } : undefined,
  } as unknown as K8sEvent;
}

// --- formatEventLabel ---

test("formatEventLabel: a mapped kind uses its short form", () => {
  expect(formatEventLabel(evt("Deployment", "api"))).toBe("deploy/api");
  expect(formatEventLabel(evt("PersistentVolumeClaim", "data"))).toBe(
    "pvc/data",
  );
});

test("formatEventLabel: an unmapped kind falls through to its own name", () => {
  // Readable rather than dropped, so a resource kind nobody has abbreviated
  // still renders. Lowercased, matching the mapped forms.
  expect(formatEventLabel(evt("Pod", "api-7d9f"))).toBe("pod/api-7d9f");
  expect(formatEventLabel(evt("CustomThing", "x"))).toBe("customthing/x");
});

test("formatEventLabel: no name yields an empty string, not a bare prefix", () => {
  // The widget treats "" as "render no prefix". Returning "deploy/" or
  // "deploy/undefined" would put a dangling slash in the row.
  expect(formatEventLabel(evt("Deployment", undefined))).toBe("");
  expect(formatEventLabel(evt(undefined, undefined))).toBe("");
});

test("formatEventLabel: a missing kind still labels by name", () => {
  expect(formatEventLabel(evt(undefined, "orphan"))).toBe("/orphan");
});

test("KIND_ABBR: keys are lowercase, so the lookup can never miss on case", () => {
  const offenders = Object.keys(KIND_ABBR).filter((k) => k !== k.toLowerCase());
  expect(offenders).toEqual([]);
});

// --- asEventList ---

test("asEventList: an array passes through unchanged", () => {
  const list = [evt("Pod", "a"), evt("Pod", "b")];
  expect(asEventList(list)).toBe(list);
});

test("asEventList: undefined and null become an empty list", () => {
  // An endpoint returning no body leaves the cached data undefined, and
  // WidgetHost's gate only rejects null -- so undefined does reach the widget
  // and would otherwise hit .map().
  expect(asEventList(undefined)).toEqual([]);
  expect(asEventList(null)).toEqual([]);
});

test("asEventList: a non-array payload becomes an empty list", () => {
  expect(asEventList({ items: [] })).toEqual([]);
  expect(asEventList("nope")).toEqual([]);
  expect(asEventList(0)).toEqual([]);
});
