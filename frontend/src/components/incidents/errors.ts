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
 * A failed create that carried a `clientRequestId` (U25c), as the
 * diagnosis-to-incident entry point sends it. A 4xx made nothing and says
 * why; `client_request_id_conflict` means the id was spent on a different
 * payload, so the next attempt sends a new one. Anything else (a network
 * error, a 5xx) may have committed, and the retry resends the same id, which
 * the server answers with the incident the first attempt made.
 */
export function keyedCreateErrorText(err: unknown): string {
  if (isPersistenceUnavailable(err)) {
    return "This deployment has no database, so incidents cannot be recorded.";
  }
  if (err instanceof ApiError) {
    if (err.reason === "client_request_id_conflict") {
      return "This create request was already used for a different incident. Try again: the next attempt sends a new request.";
    }
    if (err.status >= 400 && err.status < 500) {
      return `The incident could not be created: ${err.body?.error?.detail || err.detail || err.message}.`;
    }
  }
  return "The incident may or may not have been created. Try again: the retry resends the same request, so it cannot create a second incident.";
}
