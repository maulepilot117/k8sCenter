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
  PinConfig,
  PreferenceRecord,
  SavedViewConfig,
} from "@/lib/preference-types.ts";

export type SavedViewRecord = PreferenceRecord<SavedViewConfig>;
export type PinRecord = PreferenceRecord<PinConfig>;

const VIEWS = "/v1/preferences/views";
const PINS = "/v1/preferences/pins";

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
