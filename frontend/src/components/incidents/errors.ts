import { ApiError } from "@/lib/api.ts";
import { incidentErrorNumber } from "@/lib/incident-api.ts";

/**
 * Error wording shared by the incident workspace and its panels. Client-only:
 * it imports api.ts, so only islands (and components they render) may use it.
 */

/** The server's fixed Retry-After for `incident_busy`, in seconds. */
const BUSY_RETRY_AFTER_SECONDS = 1;

/** `what` followed by when the server asked to be retried. */
export function busyText(what: string): string {
  const s = BUSY_RETRY_AFTER_SECONDS;
  return `${what} The server asked to retry in about ${s} second${s === 1 ? "" : "s"}.`;
}

/** A write or read that failed: busy, grant limit, or the server's own detail. */
export function simpleErrorText(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.reason === "incident_busy") {
      return busyText("The incident is busy.");
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
