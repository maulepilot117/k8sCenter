import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import {
  clearPendingCaptures,
  isStaleConflict,
  newIncidentFormKey,
  PENDING_CAPTURE_PREFIX,
  PENDING_INTENT_MAX_AGE_MS,
  readPendingCapture,
  writePendingCapture,
} from "./incident-create.ts";

// Registered for this file only (sessionStorage), like pending-apply_test.ts.
beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());
afterEach(() => {
  clearPendingCaptures();
  globalThis.sessionStorage.clear();
});

const create = {
  requestId: "11111111-1111-4111-8111-111111111111",
  title: "API latency",
  summary: "p99 over 2s",
  windowStart: "2026-10-06T08:00:00.000Z",
  createdAt: 1_800_000_000_000,
  sentAt: 1_800_000_060_000,
};

test("a window end round-trips through the stored record", () => {
  const key = newIncidentFormKey("u1");
  writePendingCapture(key, {
    ...create,
    windowEnd: "2026-10-06T09:00:00.000Z",
  });
  expect(readPendingCapture(key)).toEqual({
    ...create,
    windowEnd: "2026-10-06T09:00:00.000Z",
  });
});

test("a malformed window end is dropped, the rest of the record kept", () => {
  const key = newIncidentFormKey("u1");
  globalThis.sessionStorage.setItem(
    PENDING_CAPTURE_PREFIX + key,
    JSON.stringify({ ...create, windowEnd: 42 }),
  );
  expect(readPendingCapture(key)).toEqual(create);
});

test("the form's key is per user and never one of the capture button's keys", () => {
  expect(newIncidentFormKey("u1")).not.toBe(newIncidentFormKey("u2"));
  // The button keys a record by user and four target segments.
  const buttonKey = "u1|local|team-a|Pod|web";
  writePendingCapture(buttonKey, create);
  expect(readPendingCapture(newIncidentFormKey("u1"))).toBeNull();
  expect(newIncidentFormKey("u1").split("|")).toHaveLength(2);
});

test("logout's clear removes the form's records with the button's, for every user", () => {
  writePendingCapture(newIncidentFormKey("u1"), create);
  writePendingCapture(newIncidentFormKey("u2"), { conflict: true });
  writePendingCapture("u1|local|team-a|Pod|web", create);
  clearPendingCaptures();
  expect(readPendingCapture(newIncidentFormKey("u1"))).toBeNull();
  expect(readPendingCapture(newIncidentFormKey("u2"))).toBeNull();
  expect(readPendingCapture("u1|local|team-a|Pod|web")).toBeNull();
  expect(globalThis.sessionStorage.length).toBe(0);
});

test("a conflict keeps its send time through storage; a malformed one is dropped", () => {
  const key = newIncidentFormKey("u1");
  writePendingCapture(key, { conflict: true, sentAt: 1_800_000_000_000 });
  expect(readPendingCapture(key)).toEqual({
    conflict: true,
    sentAt: 1_800_000_000_000,
  });
  globalThis.sessionStorage.setItem(
    PENDING_CAPTURE_PREFIX + key,
    JSON.stringify({ conflict: true, sentAt: "soon" }),
  );
  expect(readPendingCapture(key)).toEqual({ conflict: true });
});

test("isStaleConflict ages a conflict from its send time; none counts as stale", () => {
  const now = 1_800_000_000_000;
  const at = (ago: number) => ({ conflict: true as const, sentAt: now - ago });
  expect(isStaleConflict(at(PENDING_INTENT_MAX_AGE_MS), now)).toBe(false);
  expect(isStaleConflict(at(PENDING_INTENT_MAX_AGE_MS + 1), now)).toBe(true);
  expect(isStaleConflict({ conflict: true }, now)).toBe(true);
  // Only conflicts age here: an intent or nothing is never a stale conflict.
  expect(isStaleConflict({ ...create, sentAt: 0 }, now)).toBe(false);
  expect(isStaleConflict(null, now)).toBe(false);
});
