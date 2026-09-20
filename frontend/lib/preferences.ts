/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * It imports lib/api.ts, whose module-level access token is a process-global
 * singleton in Deno; importing this server-side would leak auth state across
 * SSR requests. Import it from islands only.
 *
 * Every call goes through api<T>() rather than apiPost/apiPut/apiDelete
 * because only api() and apiGet() accept an AbortSignal — callers need to
 * cancel in-flight preference work when the cluster or route changes.
 *
 * api<T>() already prefixes "/api", sets X-Cluster-ID from the selected
 * cluster at request time, and sets X-Requested-With on every non-GET. None
 * of those may be set here.
 */
import { type ApiError, api } from "@/lib/api.ts";
import type {
  DashboardLayoutConfig,
  DashboardScope,
} from "@/lib/dashboard/types.ts";
import type {
  PinConfig,
  PreferenceRecord,
  SavedViewConfig,
} from "@/lib/preference-types.ts";

export type SavedViewRecord = PreferenceRecord<SavedViewConfig>;
export type PinRecord = PreferenceRecord<PinConfig>;
export type LayoutRecord = PreferenceRecord<DashboardLayoutConfig>;

/**
 * What both layout endpoints return: the stored record, plus anything the
 * server removed from it on the way out.
 *
 * `withheld` names the instanceIds the read dropped because the caller can no
 * longer see their namespace, and it is `omitempty` on the Go side — an
 * unfiltered response omits the key entirely, which is why it is optional
 * here rather than an always-present empty array.
 *
 * It is not cosmetic. A client that saw only the survivors could not tell a
 * filtered layout from one the user arranged that way, so it would render a
 * dashboard quietly missing widgets and the first save after that would make
 * the loss permanent. Mirrors LayoutResponse in
 * backend/internal/preferences/handler.go.
 */
export interface LayoutResponse extends LayoutRecord {
  withheld?: string[];
}

const VIEWS = "/v1/preferences/views";
const PINS = "/v1/preferences/pins";
const LAYOUTS = "/v1/preferences/layouts";

export const preferencesApi = {
  listViews: async (signal?: AbortSignal): Promise<SavedViewRecord[]> =>
    (await api<SavedViewRecord[]>(VIEWS, { method: "GET", signal })).data ?? [],

  createView: async (
    name: string,
    config: SavedViewConfig,
    signal?: AbortSignal,
  ): Promise<SavedViewRecord> =>
    (
      await api<SavedViewRecord>(VIEWS, {
        method: "POST",
        body: JSON.stringify({ name, config }),
        signal,
      })
    ).data,

  updateView: async (
    id: string,
    name: string,
    revision: number,
    config: SavedViewConfig,
    signal?: AbortSignal,
  ): Promise<SavedViewRecord> =>
    (
      await api<SavedViewRecord>(`${VIEWS}/${encodeURIComponent(id)}`, {
        method: "PUT",
        body: JSON.stringify({ name, revision, config }),
        signal,
      })
    ).data,

  // DELETE answers 204; api() yields `{data: undefined}` for it.
  deleteView: async (id: string, signal?: AbortSignal): Promise<void> => {
    await api<void>(`${VIEWS}/${encodeURIComponent(id)}`, {
      method: "DELETE",
      signal,
    });
  },

  listPins: async (signal?: AbortSignal): Promise<PinRecord[]> =>
    (await api<PinRecord[]>(PINS, { method: "GET", signal })).data ?? [],

  createPin: async (
    name: string,
    config: PinConfig,
    signal?: AbortSignal,
  ): Promise<PinRecord> =>
    (
      await api<PinRecord>(PINS, {
        method: "POST",
        body: JSON.stringify({ name, config }),
        signal,
      })
    ).data,

  deletePin: async (id: string, signal?: AbortSignal): Promise<void> => {
    await api<void>(`${PINS}/${encodeURIComponent(id)}`, {
      method: "DELETE",
      signal,
    });
  },

  /**
   * Every dashboard layout the caller owns, on every cluster, most recently
   * updated first.
   *
   * The collection read, not a second way to fetch one layout. It exists for
   * the editor's "copy from another cluster": layouts are scoped per (user,
   * cluster, scope), and `getLayout` below can only answer for the cluster the
   * request is addressed to -- addressing one at another cluster requires the
   * admin role, so for everyone else this is the only way to see that a layout
   * elsewhere exists at all.
   *
   * Each record's `withheld` names placements the server refused to hand
   * across clusters. It answers for every cluster at once and so cannot
   * re-authorize a namespace against the cluster the layout lives on; it drops
   * those placements rather than serving them unchecked. Mirrors
   * HandleListLayouts in backend/internal/preferences/handler.go.
   */
  listLayouts: async (signal?: AbortSignal): Promise<LayoutResponse[]> =>
    (await api<LayoutResponse[]>(LAYOUTS, { method: "GET", signal })).data ??
    [],

  /**
   * The caller's layout for one dashboard scope, or null when they have not
   * customized it.
   *
   * Null is a 204, which api() surfaces as `data: undefined`. It is a normal
   * state — "this dashboard is still the shipped default" — and distinct from
   * the 400 a scope this server does not serve answers with, which arrives
   * here as a thrown ApiError. A client that collapsed the two could not tell
   * an unsaved dashboard from a mistyped one.
   */
  getLayout: async (
    scope: DashboardScope,
    signal?: AbortSignal,
  ): Promise<LayoutResponse | null> =>
    (
      await api<LayoutResponse>(`${LAYOUTS}/${encodeURIComponent(scope)}`, {
        method: "GET",
        signal,
      })
    ).data ?? null,

  /**
   * Creates or replaces the layout for one scope. 201 and 200 respectively,
   * both carrying the record.
   *
   * The body carries no `name`: the scope is the path and the record's name is
   * derived from it server-side, so sending one is a 400 naming the field.
   * `revision` is a claim about what is stored — 0 means "I believe none
   * exists" — and a wrong claim in either direction is a 409 rather than an
   * overwrite, so two tabs arranging the same dashboard cannot silently
   * discard each other's work.
   */
  saveLayout: async (
    scope: DashboardScope,
    revision: number,
    config: DashboardLayoutConfig,
    signal?: AbortSignal,
  ): Promise<LayoutResponse> =>
    (
      await api<LayoutResponse>(`${LAYOUTS}/${encodeURIComponent(scope)}`, {
        method: "PUT",
        body: JSON.stringify({ revision, config }),
        signal,
      })
    ).data,
};

/**
 * The machine-readable reason codes the preferences endpoints emit, mirrored
 * from the WriteErrorWithReason call sites in
 * backend/internal/preferences/handler.go and types.go.
 *
 * The list is declared once and the union is derived from it. Declaring both
 * by hand would let a code be added to one and not the other: the type would
 * accept a reason the runtime check rejects, and the UI would silently stop
 * classifying that error.
 */
export const PREFERENCE_REASONS = [
  "database_unavailable",
  "invalid_config",
  "unknown_resource_kind",
  "unsupported_schema_version",
  "duplicate_name",
  "already_pinned",
  "limit_reached",
  "revision_conflict",
  "invalid_name",
  "identity_too_long",
  "unknown_widget_id",
] as const;

export type PreferenceReason = (typeof PREFERENCE_REASONS)[number];

/**
 * Narrows an unknown error to a preference reason code.
 *
 * Returns undefined for anything else — a network failure, an abort, or a
 * server error carrying no reason. Callers must treat undefined as "this
 * failed for a reason we cannot name" and say so, rather than falling through
 * to a message about the last reason they handled.
 */
export function preferenceReason(e: unknown): PreferenceReason | undefined {
  const reason = (e as ApiError | undefined)?.reason;
  if (!reason) {
    return undefined;
  }
  return (PREFERENCE_REASONS as readonly string[]).includes(reason)
    ? (reason as PreferenceReason)
    : undefined;
}
