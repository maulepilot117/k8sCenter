/**
 * Create intents for incidents: the idempotency key a create is sent with
 * (U25c `clientRequestId`) and, for the diagnosis-to-incident entry point
 * (U25b), the per-target record that keeps that key across a remount.
 *
 * The record lives in sessionStorage under `PENDING_CAPTURE_PREFIX`, or in a
 * module-scoped Map when storage is unavailable (SSR, blocked storage, a
 * private window that throws, a full quota). It belongs to the session that
 * wrote it: `clearPendingCaptures` runs on logout, so the next identity on
 * this tab never resends another operator's key or captures into another
 * operator's incident. Every storage access is guarded.
 */

/**
 * A new create request id: a v4 UUID in its 36-character hyphenated form,
 * the only spelling the server accepts. `crypto.randomUUID` exists only in
 * secure contexts, and a homelab install may be served over plain HTTP, so
 * the id is built from `crypto.getRandomValues` when it is missing.
 */
export function newClientRequestId(): string {
  const c = globalThis.crypto;
  if (typeof c.randomUUID === "function") return c.randomUUID();
  const b = c.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const hex = [...b].map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/**
 * One "capture into a new incident" intent: the request id the create is
 * (re)sent with, the exact payload it was first sent with (a retry resends
 * it unchanged), and the incident it made once that is known.
 */
export interface PendingCreate {
  requestId: string;
  title: string;
  summary: string;
  windowStart: string;
  id?: string;
}

/**
 * A retry of a stored intent was refused with `client_request_id_conflict`:
 * an incident made by an earlier attempt may exist (and was since edited).
 * The key is dropped, and nothing is created until the operator chooses to.
 */
export interface PendingConflict {
  conflict: true;
}

export type PendingCapture = PendingCreate | PendingConflict;

export const isPendingConflict = (
  rec: PendingCapture | null,
): rec is PendingConflict => rec !== null && "conflict" in rec;

/** Every pending capture record lives under this prefix. */
export const PENDING_CAPTURE_PREFIX = "kubecenter.capture-pending:";

/**
 * Overrides sessionStorage when storage is unavailable. A record here is
 * newer than whatever storage holds; `null` is a tombstone for a clear that
 * storage refused.
 */
const fallback = new Map<string, PendingCapture | null>();

function parse(raw: string | null): PendingCapture | null {
  if (!raw) return null;
  try {
    const v = JSON.parse(raw) as unknown;
    if (!v || typeof v !== "object") return null;
    const { requestId, title, summary, windowStart, id, conflict } =
      v as Record<string, unknown>;
    if (conflict === true) return { conflict: true };
    if (
      typeof requestId !== "string" ||
      !requestId ||
      typeof title !== "string" ||
      typeof summary !== "string" ||
      typeof windowStart !== "string"
    ) {
      return null;
    }
    const rec: PendingCreate = { requestId, title, summary, windowStart };
    if (typeof id === "string" && id) rec.id = id;
    return rec;
  } catch {
    return null;
  }
}

/** What sessionStorage holds for the key; null when empty or unreadable. */
function stored(key: string): PendingCapture | null {
  try {
    return parse(
      globalThis.sessionStorage.getItem(PENDING_CAPTURE_PREFIX + key),
    );
  } catch {
    return null;
  }
}

export function readPendingCapture(key: string): PendingCapture | null {
  if (fallback.has(key)) {
    const rec = fallback.get(key) ?? null;
    // A tombstone is only needed while storage still holds a value.
    if (rec === null && stored(key) === null) fallback.delete(key);
    return rec;
  }
  return stored(key);
}

export function writePendingCapture(key: string, rec: PendingCapture): void {
  try {
    globalThis.sessionStorage.setItem(
      PENDING_CAPTURE_PREFIX + key,
      JSON.stringify(rec),
    );
    fallback.delete(key);
  } catch {
    // Storage unavailable or full: the fallback holds it, and shadows any
    // older value storage still has.
    fallback.set(key, rec);
  }
}

export function clearPendingCapture(key: string): void {
  try {
    globalThis.sessionStorage.removeItem(PENDING_CAPTURE_PREFIX + key);
    fallback.delete(key);
  } catch {
    // Storage refused the removal: a tombstone shadows what it still holds,
    // or, when it holds nothing readable, there is nothing to shadow.
    if (stored(key) === null) fallback.delete(key);
    else fallback.set(key, null);
  }
}

/**
 * Forgets every pending capture record, in storage and in memory. Called on
 * logout: a key and an incident id recorded for one identity must not
 * outlive that identity's session.
 */
export function clearPendingCaptures(): void {
  const doomed: string[] = [];
  try {
    const storage = globalThis.sessionStorage;
    for (let i = 0; i < storage.length; i++) {
      const k = storage.key(i);
      if (k?.startsWith(PENDING_CAPTURE_PREFIX)) {
        doomed.push(k.slice(PENDING_CAPTURE_PREFIX.length));
      }
    }
  } catch {
    // Storage unavailable: only the in-memory records exist.
  }
  fallback.clear();
  for (const key of doomed) clearPendingCapture(key);
}
