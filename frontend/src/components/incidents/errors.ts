import { ApiError } from "@/lib/api.ts";
import {
  incidentErrorNumber,
  isPersistenceUnavailable,
} from "@/lib/incident-api.ts";

/**
 * Error wording shared by the incident workspace and its panels. Client-only:
 * it imports api.ts, so only islands (and components they render) may use it.
 */

/** Used when a busy answer carries no readable Retry-After (the server sends 1). */
const DEFAULT_RETRY_AFTER_SECONDS = 1;

/** The Retry-After of a busy answer, in whole seconds, when it is a number. */
function retryAfterSeconds(err?: ApiError): number {
  const raw = err?.headers?.get("Retry-After");
  const n = raw ? Number(raw) : Number.NaN;
  return Number.isFinite(n) && n >= 0 ? n : DEFAULT_RETRY_AFTER_SECONDS;
}

/** `what` followed by when the server asked to be retried (its Retry-After). */
export function busyText(what: string, err?: ApiError): string {
  const s = retryAfterSeconds(err);
  return `${what} The server asked to retry in about ${s} second${s === 1 ? "" : "s"}.`;
}

/** A write or read that failed: busy, grant limit, or the server's own detail. */
export function simpleErrorText(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.reason === "incident_busy") {
      return busyText("The incident is busy.", err);
    }
    if (err.reason === "grant_limit_reached") {
      const max = incidentErrorNumber(err, "max");
      return `This incident is already shared with the maximum number of people${max !== undefined ? ` (${max})` : ""}.`;
    }
    if (err.status === 400 || err.status === 403 || err.status === 404) {
      return err.body?.error?.detail || err.detail || fallback;
    }
  }
  return fallback;
}

/**
 * True when a create that carried a `clientRequestId` was refused for a
 * reason that proves no attempt with that key and payload ever made an
 * incident, so the key may be dropped: a 400 or 413 (the body, the key or
 * the payload is invalid; the server validates before it writes, so the
 * same payload never committed) and the no-database 503.
 * `client_request_id_conflict` is not here: it means an incident with the
 * key DOES exist, and is handled on its own (`isCreateConflict`). Every
 * other failure (a network error, a 5xx, `incident_busy`, 401, 403, 408,
 * 429 and any other 4xx) keeps the key, because an earlier attempt may have
 * committed and resending the same key cannot create a second incident.
 */
export function createRefusedForGood(err: unknown): boolean {
  if (isPersistenceUnavailable(err)) return true;
  return err instanceof ApiError && (err.status === 400 || err.status === 413);
}

/** A retried create whose key now names an incident with a different payload. */
export function isCreateConflict(err: unknown): boolean {
  return err instanceof ApiError && err.reason === "client_request_id_conflict";
}

/**
 * The notice for `client_request_id_conflict`: an earlier attempt of this
 * create may have made an incident that was edited since. The caller drops
 * the key, links to the incident list and offers an explicit "Create a new
 * incident anyway" rather than creating on the next click.
 */
export function createConflictText(subject: string): string {
  return `An incident for ${subject} may already exist — check your incidents.`;
}

/**
 * A failed create that carried a `clientRequestId` (U25c). A refusal that
 * proves nothing was made says why; a conflict says an incident may already
 * exist (`createConflictText`); anything else may have committed, and the
 * retry resends the same key, which the server answers with the incident
 * the first attempt made.
 */
export function keyedCreateErrorText(
  err: unknown,
  subject = "this capture",
): string {
  if (isPersistenceUnavailable(err)) {
    return "This deployment has no database, so incidents cannot be recorded.";
  }
  if (isCreateConflict(err)) return createConflictText(subject);
  if (err instanceof ApiError && err.reason === "incident_busy") {
    return `${busyText("The incident store is busy.", err)} Retrying resends the same request, so it cannot create a second incident.`;
  }
  if (err instanceof ApiError && err.status >= 400 && err.status < 500) {
    const why = err.body?.error?.detail || err.detail || err.message;
    if (createRefusedForGood(err)) {
      return `The incident could not be created: ${why}.`;
    }
    // This attempt was refused, but an earlier one may have committed.
    return `This attempt was refused: ${why}. Try again: the retry resends the same request, so it cannot create a second incident.`;
  }
  return "The incident may or may not have been created. Try again: the retry resends the same request, so it cannot create a second incident.";
}
