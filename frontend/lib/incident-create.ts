/**
 * Create intents for incidents: the idempotency key a create is sent with
 * (U25c `clientRequestId`) and the pending record that keeps that key across
 * a remount or a reload. Two callers keep one: the diagnosis-to-incident
 * entry point (U25b), one record per user and target, and the "New
 * incident" form on the incident list, one record per user
 * (`newIncidentFormKey`).
 *
 * The record lives in sessionStorage under `PENDING_CAPTURE_PREFIX`, or in a
 * module-scoped Map when storage is unavailable (SSR, blocked storage, a
 * private window that throws, a full quota). Every storage access is
 * guarded. sessionStorage is per tab: another tab neither sees nor resends
 * this tab's record (a duplicated tab starts with a copy, and resending the
 * same key from both is safe, since the server never makes two incidents
 * for one key).
 *
 * Whose record it is: the caller builds the key from the signed-in user's
 * id, so another identity on the same tab never reads it, even when the
 * session ended without `logout()` (a failed refresh redirects to the login
 * page directly). `clearPendingCaptures` runs on logout and removes every
 * identity's records, the form's included; a record left behind by a
 * session that ended otherwise stays unread until the tab closes, unless the
 * same user signs in again.
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
 * One create intent: the request id the create is (re)sent with, the
 * payload it was last sent with, when the intent was made and when it was
 * last sent (epoch ms; `sentAt` is client-only and restamped on every send),
 * and the incident it made once that is known.
 *
 * The capture button resends the stored payload unchanged and records `id`;
 * the New-incident form prefills from the payload, resends the id with
 * whatever the inputs then are, never records `id` (a success navigates
 * away and clears the record), and alone sets `windowEnd`.
 */
export interface PendingCreate {
  requestId: string;
  title: string;
  summary: string;
  windowStart: string;
  windowEnd?: string;
  createdAt: number;
  sentAt?: number;
  id?: string;
}

/**
 * An incident for this capture may already exist, and nothing is created
 * until the operator chooses to: a retry was refused with
 * `client_request_id_conflict` (an earlier attempt made an incident that
 * was edited since), or the target was captured into an existing incident
 * while a create's outcome was still unknown. The key is dropped.
 */
export interface PendingConflict {
  conflict: true;
}

export type PendingCapture = PendingCreate | PendingConflict;

export const isPendingConflict = (
  rec: PendingCapture | null,
): rec is PendingConflict => rec !== null && "conflict" in rec;

/**
 * How long after its LAST send an intent whose create outcome is unknown
 * (no incident id yet) is still resent silently. Past it no create can
 * still be in flight (the proxy gives up after 30 s), and an operator
 * returning to it much later should decide rather than have an old intent
 * replayed: the record then reads like a conflict, an incident for this
 * capture may already exist. Measured from `sentAt`, falling back to
 * `createdAt`, so an intent retried a moment ago is never stale however
 * long ago it was first sent.
 */
export const PENDING_INTENT_MAX_AGE_MS = 15 * 60 * 1000;

/**
 * True for an id-less intent last sent more than `PENDING_INTENT_MAX_AGE_MS`
 * ago. An intent with a known incident id is never stale: retrying it only
 * captures into that incident.
 */
export function isStaleIntent(rec: PendingCapture | null, now: number) {
  return (
    rec !== null &&
    !isPendingConflict(rec) &&
    !rec.id &&
    now - (rec.sentAt ?? rec.createdAt) > PENDING_INTENT_MAX_AGE_MS
  );
}

/** Every pending create record, the button's and the form's, lives under this prefix. */
export const PENDING_CAPTURE_PREFIX = "kubecenter.capture-pending:";

/**
 * The New-incident form's record key for `userId`. The capture button's keys
 * are the user id followed by four target segments, so this two-segment key
 * never names one of them.
 */
export const newIncidentFormKey = (userId: string) =>
  `${userId}|new-incident-form`;

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
    const {
      requestId,
      title,
      summary,
      windowStart,
      windowEnd,
      createdAt,
      sentAt,
      id,
      conflict,
    } = v as Record<string, unknown>;
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
    // A record without a valid time (an older format) counts as stale.
    const rec: PendingCreate = {
      requestId,
      title,
      summary,
      windowStart,
      createdAt:
        typeof createdAt === "number" && Number.isFinite(createdAt)
          ? createdAt
          : 0,
    };
    if (typeof windowEnd === "string" && windowEnd) rec.windowEnd = windowEnd;
    if (typeof sentAt === "number" && Number.isFinite(sentAt)) {
      rec.sentAt = sentAt;
    }
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
 * Forgets every pending create record (the capture button's and the
 * New-incident form's), in storage and in memory. Called on logout: a key
 * and an incident id recorded for one identity must not outlive that
 * identity's session.
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
