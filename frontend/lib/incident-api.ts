/**
 * Typed client for the incident endpoints (`/v1/incidents`, Release D).
 * Stateless: every function is one request over `api.ts`, returns the
 * unwrapped payload, and throws `ApiError` on failure. Read a failure through
 * `ApiError.status` and `ApiError.reason` (an `IncidentErrorReason`), plus
 * `errorExtra()` for string extras and `incidentErrorNumber()` for numeric
 * ones (`currentRevision`, `max`, `current`, `attempted`), never `body.error`.
 *
 * **Which cluster a request is addressed to.**
 *
 *   - Everything except capture is pinned to the local cluster, whatever the
 *     operator has selected. An incident is a record stored in this
 *     installation's database, not an object on a cluster: the handlers
 *     authorize each evidence row against the cluster STORED on the row and
 *     never read the request's X-Cluster-ID. Left on the ambient selection, a
 *     non-admin viewing a remote cluster would be refused by the cluster-access
 *     gate before the incident was even looked up. This is the same decision
 *     `change-api.ts` makes for receipt reads.
 *   - `captureEvidence` is the exception. It reads a live object, and Release
 *     D captures from the local cluster only. The caller names the cluster the
 *     target was observed on and it is sent as X-Cluster-ID, so a remote target
 *     is refused by the server instead of being silently resolved to a
 *     same-named object on the local cluster. How it is refused depends on the
 *     caller; see `captureEvidence` and `isRemoteCaptureRefusal`.
 *
 * Paging is cursor-based: pass the previous page's `continue` back; a page
 * without one is the last.
 *
 * Client-only, like `api.ts`.
 */

import {
  ApiError,
  apiBlob,
  apiDelete,
  apiGet,
  apiPost,
  apiPut,
} from "./api.ts";
import { LOCAL_CLUSTER_ID } from "./cluster.ts";
import {
  type CaptureRequest,
  type CaptureResponse,
  type CreateIncidentRequest,
  type EvidencePage,
  type ExportFormat,
  type GrantRequest,
  type GrantView,
  type IncidentDetail,
  type IncidentSummary,
  type IncidentView,
  isDotSegment,
  isIncidentId,
  type NoteView,
  type UpdateIncidentRequest,
} from "./incident-types.ts";

/** Incident records are not bound to the selected cluster; see the module doc. */
const INCIDENT_CLUSTER = LOCAL_CLUSTER_ID;

/** Cursor paging for every list; omitted fields take the server's defaults. */
export interface PageParams {
  limit?: number;
  /** The previous page's `continue`. */
  continue?: string;
}

/** One page of a cursor-paged list. */
export interface CursorPage<T> {
  items: T[];
  /** Present when another page follows. */
  continue?: string;
}

/**
 * Builds a list query string: `""` when nothing is set, else `?` plus `limit`
 * then `continue`. An empty cursor is not sent.
 */
export function buildIncidentPageQuery(params: PageParams = {}): string {
  const query = new URLSearchParams();
  if (params.limit !== undefined) query.set("limit", String(params.limit));
  if (params.continue) query.set("continue", params.continue);
  return query.size > 0 ? `?${query}` : "";
}

/**
 * Thrown, before any request is made, for an incident or note id that is not
 * a UUID. The handlers would 400 it anyway; refusing it here means no id can
 * put `..` or another path segment into a request.
 */
export class InvalidIncidentIdError extends Error {
  constructor(what: "incident" | "note") {
    super(`invalid ${what} id`);
    this.name = "InvalidIncidentIdError";
  }
}

/**
 * Thrown, before any request is made, for a grantee id of "." or "..". As
 * the last path segment of a revoke either would be normalized into another
 * resource (".." makes it DELETE /incidents/{id}). The server refuses to
 * create such a grant; this refuses to address one.
 */
export class UnsafeGranteeIdError extends Error {
  constructor() {
    super("grantee id cannot be used in a request path");
    this.name = "UnsafeGranteeIdError";
  }
}

function base(id: string): string {
  if (!isIncidentId(id)) throw new InvalidIncidentIdError("incident");
  return `/v1/incidents/${id}`;
}

function notePath(id: string, noteId: string): string {
  if (!isIncidentId(noteId)) throw new InvalidIncidentIdError("note");
  return `${base(id)}/notes/${noteId}`;
}
const pinned = (signal?: AbortSignal) => ({
  clusterId: INCIDENT_CLUSTER,
  signal,
});

// --- Incidents ---------------------------------------------------------------

/**
 * `GET /v1/incidents`: incidents the caller owns or holds a grant on, newest
 * first, each with the caller's role. Rows carry no evidence counts.
 */
export async function listIncidents(
  params: PageParams = {},
  signal?: AbortSignal,
): Promise<CursorPage<IncidentView>> {
  const res = await apiGet<IncidentView[]>(
    `/v1/incidents${buildIncidentPageQuery(params)}`,
    pinned(signal),
  );
  return {
    items: res.data ?? [],
    continue: res.metadata?.continue || undefined,
  };
}

/** `POST /v1/incidents`: opens an incident owned by the caller on the local cluster. */
export async function createIncident(
  body: CreateIncidentRequest,
  signal?: AbortSignal,
): Promise<IncidentSummary> {
  const res = await apiPost<IncidentSummary>(
    "/v1/incidents",
    body,
    pinned(signal),
  );
  return res.data;
}

/**
 * `GET /v1/incidents/{id}`: the record, the caller's whole-incident counts and
 * one page of evidence; `params` pages the evidence. An incident the caller
 * may not see is a 404, exactly like a missing one.
 */
export async function getIncident(
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
): Promise<{ detail: IncidentDetail; continue?: string }> {
  const res = await apiGet<IncidentDetail>(
    `${base(id)}${buildIncidentPageQuery(params)}`,
    pinned(signal),
  );
  return { detail: res.data, continue: res.metadata?.continue || undefined };
}

/** `PUT /v1/incidents/{id}` (owner only): an omitted field keeps its value. */
export async function updateIncident(
  id: string,
  body: UpdateIncidentRequest,
  signal?: AbortSignal,
): Promise<IncidentSummary> {
  const res = await apiPut<IncidentSummary>(base(id), body, pinned(signal));
  return res.data;
}

/** `DELETE /v1/incidents/{id}` (owner only): removes everything under it. */
export async function deleteIncident(
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  await apiDelete(base(id), pinned(signal));
}

// --- Evidence and capture -------------------------------------------------------

/** `GET /v1/incidents/{id}/evidence`: one filtered page plus the caller's counts. */
export async function listEvidence(
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
): Promise<{ page: EvidencePage; continue?: string }> {
  const res = await apiGet<EvidencePage>(
    `${base(id)}/evidence${buildIncidentPageQuery(params)}`,
    pinned(signal),
  );
  return { page: res.data, continue: res.metadata?.continue || undefined };
}

/**
 * `POST /v1/incidents/{id}/capture` (owner only): collects evidence about one
 * object on `clusterId`, the cluster the target was observed on. Only the
 * local cluster is accepted (see the module doc). A remote `clusterId` is
 * refused in one of two shapes, and nothing is collected in either:
 *
 *   - an admin passes the cluster-access gate and the handler answers 400
 *     with reason `remote_capture_unsupported`;
 *   - anyone else is stopped earlier by the ClusterContext middleware with a
 *     reason-less 403 ("admin role required for remote cluster access").
 *
 * Use `isRemoteCaptureRefusal(err, clusterId)` to recognize both. Better
 * still, a capture UI should not offer capture while the target's cluster is
 * not the local one, so neither request is sent.
 *
 * That 403 also fires api.ts's `onForbidden` callback, like every non-auth
 * 403. In this app the callback (lib/auth.ts) only re-reads RBAC for the
 * selected namespace through `GET /v1/auth/me`; it never logs the user out or
 * redirects, and its re-entry guard collapses bursts. With a remote cluster
 * selected that re-read can itself 403 for a non-admin, which leaves the RBAC
 * summary unset (permissive until the next read), the same as for any other
 * remote 403. That is the existing global behaviour, so it is not changed
 * here.
 */
export async function captureEvidence(
  id: string,
  clusterId: string,
  body: CaptureRequest,
  signal?: AbortSignal,
): Promise<CaptureResponse> {
  const res = await apiPost<CaptureResponse>(`${base(id)}/capture`, body, {
    clusterId,
    signal,
  });
  return res.data;
}

// --- Notes -------------------------------------------------------------------

/** `GET /v1/incidents/{id}/notes`: one page, oldest first. */
export async function listNotes(
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
): Promise<CursorPage<NoteView>> {
  const res = await apiGet<NoteView[]>(
    `${base(id)}/notes${buildIncidentPageQuery(params)}`,
    pinned(signal),
  );
  return {
    items: res.data ?? [],
    continue: res.metadata?.continue || undefined,
  };
}

/** `POST /v1/incidents/{id}/notes` (owner, or a grant with canAnnotate). */
export async function createNote(
  id: string,
  body: string,
  signal?: AbortSignal,
): Promise<NoteView> {
  const res = await apiPost<NoteView>(
    `${base(id)}/notes`,
    { body },
    pinned(signal),
  );
  return res.data;
}

/**
 * `PUT /v1/incidents/{id}/notes/{noteId}` (author only). `revision` is the
 * one the caller last read; a stale one is 409 `note_revision_conflict` with
 * `currentRevision` in the extras.
 */
export async function updateNote(
  id: string,
  noteId: string,
  body: string,
  revision: number,
  signal?: AbortSignal,
): Promise<NoteView> {
  const res = await apiPut<NoteView>(
    notePath(id, noteId),
    { body, revision },
    pinned(signal),
  );
  return res.data;
}

/** `DELETE /v1/incidents/{id}/notes/{noteId}` (author only). */
export async function deleteNote(
  id: string,
  noteId: string,
  signal?: AbortSignal,
): Promise<void> {
  await apiDelete(notePath(id, noteId), pinned(signal));
}

// --- Grants ------------------------------------------------------------------

/** `GET /v1/incidents/{id}/grants` (owner only): at most 50, so unpaged. */
export async function listGrants(
  id: string,
  signal?: AbortSignal,
): Promise<GrantView[]> {
  const res = await apiGet<GrantView[]>(`${base(id)}/grants`, pinned(signal));
  return res.data ?? [];
}

/**
 * `POST /v1/incidents/{id}/grants` (owner only): grants, or updates
 * `canAnnotate` on an existing grant. Returns null when the grantee is the
 * caller (a 204 no-op: the owner already holds every right).
 */
export async function addGrant(
  id: string,
  body: GrantRequest,
  signal?: AbortSignal,
): Promise<GrantView | null> {
  const res = await apiPost<GrantView>(
    `${base(id)}/grants`,
    body,
    pinned(signal),
  );
  return res.data ?? null;
}

/**
 * `DELETE /v1/incidents/{id}/grants/{granteeId}` (owner only). Grantee ids
 * are user ids and may contain `/` or `%`, so the segment is percent-encoded;
 * a grantee without a grant is a 404.
 */
export async function removeGrant(
  id: string,
  granteeId: string,
  signal?: AbortSignal,
): Promise<void> {
  if (isDotSegment(granteeId)) throw new UnsafeGranteeIdError();
  await apiDelete(
    `${base(id)}/grants/${encodeURIComponent(granteeId)}`,
    pinned(signal),
  );
}

// --- Export ------------------------------------------------------------------

/** A downloaded export: the file and the name the server gave it. */
export interface IncidentExport {
  blob: Blob;
  filename: string;
}

/**
 * `GET /v1/incidents/{id}/export?format=`: the incident as a file. The
 * response is not an API envelope and the access token lives in memory, so a
 * plain link cannot download it; this fetches it through `apiBlob` (bearer
 * token, 401 refresh-and-retry, ApiError on failure). The filename comes from
 * Content-Disposition, else `incident-<id>.<ext>`.
 *
 * The server sets no header saying the export was truncated (the flag is
 * inside the JSON body and the Markdown text), so this does not report it.
 */
export async function exportIncident(
  id: string,
  format: ExportFormat,
  signal?: AbortSignal,
): Promise<IncidentExport> {
  const res = await apiBlob(
    `${base(id)}/export?format=${encodeURIComponent(format)}`,
    pinned(signal),
  );
  const match = /filename="([^"]+)"/.exec(
    res.headers.get("Content-Disposition") ?? "",
  );
  return {
    blob: res.blob,
    filename:
      match?.[1] ?? `incident-${id}.${format === "markdown" ? "md" : "json"}`,
  };
}

// --- Error helpers -------------------------------------------------------------

/**
 * Numeric counterpart of `errorExtra`: the extra when it is a JSON number,
 * else undefined (`currentRevision`, and the limit extras `max`, `current`,
 * `attempted`).
 */
export function incidentErrorNumber(
  err: ApiError,
  key: string,
): number | undefined {
  const v = err.body?.error?.extra?.[key];
  return typeof v === "number" ? v : undefined;
}

/**
 * True when the deployment has no PostgreSQL, so incidents cannot exist at
 * all. That is a property of the installation, not an empty list and not a
 * transient failure: show it as such, and offer neither create nor retry.
 */
export function isPersistenceUnavailable(err: unknown): boolean {
  return (
    err instanceof ApiError && err.reason === "incident_persistence_unavailable"
  );
}

/**
 * True when a `captureEvidence` call to `clusterId` was refused because the
 * target is not on the local cluster: either the handler's 400
 * `remote_capture_unsupported` (admin callers) or the cluster middleware's
 * reason-less 403 (everyone else; see `captureEvidence`).
 *
 * The 403 carries no reason, so it is attributed by the cluster the request
 * was sent to: a reason-less 403 on a capture addressed to a non-local
 * cluster. For a local `clusterId` a 403 is the owner-only gate and is never
 * reported here. One overlap remains: an admin who is a collaborator, not the
 * owner, gets the owner-only 403 before the cluster check runs. Capture is an
 * owner control, so a UI that offers it only to owners never sees that case.
 */
export function isRemoteCaptureRefusal(
  err: unknown,
  clusterId: string,
): boolean {
  if (!(err instanceof ApiError)) return false;
  if (err.reason === "remote_capture_unsupported") return true;
  // The middleware reads an empty X-Cluster-ID as the local cluster too.
  const local = clusterId === "" || clusterId === INCIDENT_CLUSTER;
  return err.status === 403 && !err.reason && !local;
}
