import { describe, expect, test } from "bun:test";
import type {
  AssuranceCollection,
  AssuranceCondition,
  AssuranceStatus,
  SurfaceState,
} from "./backup-assurance-types.ts";
import {
  ASSURANCE_CONDITIONS,
  conditionExplanation,
  conditionLabel,
  countsAreCurrent,
  HONESTY_TEXT,
  surfaceStateCopy,
  surfaceStateFor,
} from "./backup-assurance-types.ts";

function status(
  collection: AssuranceCollection,
  over: Partial<AssuranceStatus> = {},
): AssuranceStatus {
  const byCondition = Object.fromEntries(
    ASSURANCE_CONDITIONS.map((c) => [c, 0]),
  ) as Record<AssuranceCondition, number>;
  return {
    enabled: true,
    collection,
    collectionSource: "this_replica",
    policyCount: 1,
    open: { total: 0, byCondition },
    ...over,
  };
}

const HONESTY_FORBIDDEN = /recoverab|restorable|guarantee|protected/i;
const SIX_STATES: SurfaceState[] = [
  "ok",
  "stale",
  "empty",
  "unknown",
  "forbidden",
  "unavailable",
];

describe("surfaceStateFor", () => {
  test("never upgrades a non-ok collection, in particular unknown to empty", () => {
    const collections: AssuranceCollection[] = [
      "stale",
      "empty",
      "unknown",
      "unavailable",
    ];
    for (const c of collections) {
      expect(surfaceStateFor(status(c))).toBe(c);
    }
    expect(surfaceStateFor(status("unknown"))).not.toBe("empty");
    expect(surfaceStateFor(status("unknown"))).not.toBe("ok");
  });

  test("passes an ok collection through", () => {
    expect(surfaceStateFor(status("ok"))).toBe("ok");
  });

  test("an unreadable status is unknown, never empty", () => {
    expect(surfaceStateFor(null)).toBe("unknown");
    expect(surfaceStateFor(null, 500)).toBe("unknown");
  });

  test("403 is forbidden and outranks the status body", () => {
    expect(surfaceStateFor(null, 403)).toBe("forbidden");
    expect(surfaceStateFor(status("ok"), 403)).toBe("forbidden");
  });

  test("503 and a disabled collector are unavailable", () => {
    expect(surfaceStateFor(null, 503)).toBe("unavailable");
    expect(surfaceStateFor(status("ok"), 503)).toBe("unavailable");
    expect(surfaceStateFor(status("ok", { enabled: false }))).toBe(
      "unavailable",
    );
  });

  test("only an ok collection makes counts current", () => {
    for (const s of SIX_STATES) {
      expect(countsAreCurrent(s)).toBe(s === "ok");
    }
  });
});

describe("conditionLabel / conditionExplanation", () => {
  test("every condition has a distinct, non-empty label and explanation", () => {
    const labels = new Set<string>();
    for (const c of ASSURANCE_CONDITIONS) {
      const label = conditionLabel(c);
      expect(label.length).toBeGreaterThan(0);
      // The lookup falls back to the raw key; a missing entry would echo it.
      expect(label).not.toBe(c);
      expect(conditionExplanation(c).length).toBeGreaterThan(0);
      labels.add(label);
    }
    expect(labels.size).toBe(ASSURANCE_CONDITIONS.length);
  });

  test("the condition list has no duplicates", () => {
    expect(new Set(ASSURANCE_CONDITIONS).size).toBe(
      ASSURANCE_CONDITIONS.length,
    );
  });

  test("no explanation or label claims recoverability", () => {
    for (const c of ASSURANCE_CONDITIONS) {
      expect(conditionExplanation(c)).not.toMatch(HONESTY_FORBIDDEN);
      expect(conditionLabel(c)).not.toMatch(HONESTY_FORBIDDEN);
    }
  });

  test("collection_unknown says state is unknown and never that no backups exist", () => {
    const text = conditionExplanation("collection_unknown");
    expect(text).toContain("unknown");
    expect(text).not.toMatch(/no backups/i);
  });
});

describe("honesty copy", () => {
  test("the banner states what assurance does not verify", () => {
    expect(HONESTY_TEXT).toContain(
      "does not verify that a restore would succeed",
    );
    expect(HONESTY_TEXT).toContain("No restore has been attempted");
  });

  test("surface-state copy covers all six states without recoverability or no-backups claims", () => {
    for (const s of SIX_STATES) {
      const { word, sentence } = surfaceStateCopy(s);
      expect(word).toBe(s);
      expect(sentence.length).toBeGreaterThan(0);
      expect(sentence).not.toMatch(HONESTY_FORBIDDEN);
      expect(sentence).not.toMatch(/no backups/i);
    }
  });
});
