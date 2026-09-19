/**
 * Pure, dependency-free preference contract. Safe to import from routes,
 * components and islands alike — it touches no browser API and no module
 * state. All network access lives in lib/preferences.ts.
 *
 * The allowlists here MUST stay in lockstep with
 * backend/internal/preferences/types.go. The server is authoritative; these
 * copies exist so the UI can refuse an impossible save before a round trip
 * and can explain a stored value that has drifted out of the allowlist.
 */

export const SAVED_VIEW_SCHEMA_VERSION = 1;
export const PIN_SCHEMA_VERSION = 1;
export const MAX_SAVED_VIEWS = 100;
export const MAX_PINS = 200;

/**
 * Length bounds the server enforces, so a save affordance can refuse before a
 * round trip instead of surfacing a 400. Both count CHARACTERS, matching Go's
 * utf8.RuneCountInString — measure with `[...s].length`, never `s.length`,
 * which counts UTF-16 units and would accept a name the server rejects.
 */
export const MAX_RECORD_NAME_LEN = 128;
export const MAX_SEARCH_LEN = 256;

export const SAVED_VIEW_STATUS_FILTERS = [
  "all",
  "running",
  "pending",
  "failed",
  "progressing",
] as const;

/**
 * Mirrors the ResourceTable comparator in islands/ResourceTable.tsx, which
 * sorts only these three keys. Widening this list requires widening that
 * comparator first — otherwise a saved view would restore a sortKey the table
 * silently falls back to "name" for, and the user would see a sort they never
 * chose. `SAVED_VIEW_SORT_KEYS matches the ResourceTable comparator` in the
 * test file is the reminder.
 */
export const SAVED_VIEW_SORT_KEYS = ["name", "namespace", "age"] as const;
export const SAVED_VIEW_SORT_DIRS = ["asc", "desc"] as const;

export type StatusFilter = (typeof SAVED_VIEW_STATUS_FILTERS)[number];
export type SortKey = (typeof SAVED_VIEW_SORT_KEYS)[number];
export type SortDir = (typeof SAVED_VIEW_SORT_DIRS)[number];

const DEFAULT_STATUS_FILTER: StatusFilter = "all";
const DEFAULT_SORT_KEY: SortKey = "name";
const DEFAULT_SORT_DIR: SortDir = "asc";

export interface SavedViewConfig {
  schemaVersion: number;
  resourceKind: string;
  namespace: string; // "" = all namespaces
  search: string;
  statusFilter: StatusFilter;
  sortKey: SortKey;
  sortDir: SortDir;
}

export interface PinConfig {
  schemaVersion: number;
  resourceKind: string;
  group: string; // reserved-empty in Release A
  version: string; // reserved-empty in Release A
  namespace: string;
  name: string;
  uid: string;
  displayKind: string;
}

export interface PreferenceRecord<C> {
  id: string;
  kind: "saved_view" | "pin" | "dashboard_layout";
  name: string;
  clusterId: string;
  schemaVersion: number;
  revision: number;
  config: C;
  createdAt: string;
  updatedAt: string;
}

/** Table state a saved view can capture — exactly what ResourceTable holds. */
export interface TableViewState {
  resourceKind: string;
  namespace: string;
  search: string;
  statusFilter: string;
  sortKey: string;
  sortDir: string;
}

function isStatusFilter(v: string): v is StatusFilter {
  return (SAVED_VIEW_STATUS_FILTERS as readonly string[]).includes(v);
}

function isSortKey(v: string): v is SortKey {
  return (SAVED_VIEW_SORT_KEYS as readonly string[]).includes(v);
}

function isSortDir(v: string): v is SortDir {
  return (SAVED_VIEW_SORT_DIRS as readonly string[]).includes(v);
}

/**
 * Snapshots live table state into a storable config.
 *
 * Unsupported values are coerced to their defaults rather than sent to a
 * server that would reject the whole save with a 400. Today's ResourceTable
 * cannot produce one — its chips lowercase to exactly the allowlist and its
 * header sorts exactly the three comparator keys — so this is a guard against
 * a future chip or column, not a path the UI walks. The restore direction is
 * where drift actually arrives (a record stored by another version), and
 * applyViewState reports every degradation it makes there.
 */
export function captureViewState(s: TableViewState): SavedViewConfig {
  return {
    schemaVersion: SAVED_VIEW_SCHEMA_VERSION,
    resourceKind: s.resourceKind,
    namespace: s.namespace,
    search: s.search,
    statusFilter: isStatusFilter(s.statusFilter)
      ? s.statusFilter
      : DEFAULT_STATUS_FILTER,
    sortKey: isSortKey(s.sortKey) ? s.sortKey : DEFAULT_SORT_KEY,
    sortDir: isSortDir(s.sortDir) ? s.sortDir : DEFAULT_SORT_DIR,
  };
}

/**
 * Restores a stored config onto table state, reporting every value that had
 * to be degraded. Callers MUST surface `warnings` — silently coercing an
 * unsupported sortKey to "name" would violate R3 (missing observations must
 * not appear healthy).
 */
export function applyViewState(c: SavedViewConfig): {
  state: TableViewState;
  warnings: string[];
} {
  const warnings: string[] = [];

  if (!isSupportedSavedViewSchema(c.schemaVersion)) {
    // Reported, not refused: the fields below are still read on a best-effort
    // basis so a user on an older build can still open the view they saved on
    // a newer one — but they are told the record was written by a version
    // this build does not understand.
    warnings.push(
      `unsupported saved schema version ${c.schemaVersion}; this build understands ${SAVED_VIEW_SCHEMA_VERSION}`,
    );
  }

  let statusFilter: StatusFilter = DEFAULT_STATUS_FILTER;
  if (isStatusFilter(c.statusFilter)) {
    statusFilter = c.statusFilter;
  } else {
    warnings.push(
      `unsupported status filter "${c.statusFilter}"; showing all resources instead`,
    );
  }

  let sortKey: SortKey = DEFAULT_SORT_KEY;
  if (isSortKey(c.sortKey)) {
    sortKey = c.sortKey;
  } else {
    warnings.push(
      `unsupported sort key "${c.sortKey}"; sorting by ${DEFAULT_SORT_KEY} instead`,
    );
  }

  let sortDir: SortDir = DEFAULT_SORT_DIR;
  if (isSortDir(c.sortDir)) {
    sortDir = c.sortDir;
  } else {
    warnings.push(
      `unsupported sort direction "${c.sortDir}"; sorting ${DEFAULT_SORT_DIR} instead`,
    );
  }

  return {
    state: {
      resourceKind: c.resourceKind,
      namespace: c.namespace,
      search: c.search,
      statusFilter,
      sortKey,
      sortDir,
    },
    warnings,
  };
}

export function isSupportedSavedViewSchema(v: number): boolean {
  return v === SAVED_VIEW_SCHEMA_VERSION;
}

export function isSupportedPinSchema(v: number): boolean {
  return v === PIN_SCHEMA_VERSION;
}

/**
 * "<resourceKind>/<namespace>/<name>" — UID excluded, matching the server's
 * PinDedupKey. A deleted-and-recreated object keeps its kind, namespace and
 * name but gets a fresh uid; excluding the uid makes the recreation collide
 * with the existing pin instead of appearing as a duplicate the user never
 * created. The uid rides inside the config as evidence of which object was
 * pinned, and classifyPin is what turns that evidence into a state.
 */
export function pinDedupKey(c: PinConfig): string {
  return `${c.resourceKind}/${c.namespace}/${c.name}`;
}

export type PinLiveState =
  | "ok"
  | "replaced"
  | "missing"
  | "forbidden"
  | "unknown";

/**
 * Compares a stored pin against a live lookup outcome. `liveUid` is the
 * fetched object's metadata.uid, or null when the fetch failed.
 * A stored pin with an empty uid resolves "unknown", never "ok" — an
 * un-evidenced pin must not be presented as verified (R3).
 *
 * A pin stored under a schema this build does not understand is also
 * "unknown", for the same reason: this build cannot know that a future
 * schema still means by `uid` what v1 means by it, so a matching string is
 * not evidence the pin points at the object the user pinned. The lookup
 * outcomes that do not depend on reading the stored envelope — the target is
 * gone, or the user may no longer read it — are still reported as themselves,
 * because those facts hold whatever the schema says.
 */
export function classifyPin(
  stored: PinConfig,
  outcome: {
    status: "ok" | "notFound" | "forbidden" | "error";
    liveUid?: string;
  },
): PinLiveState {
  switch (outcome.status) {
    case "notFound":
      return "missing";
    case "forbidden":
      // Distinct from "missing" on purpose: a revoked permission and a deleted
      // object are different facts, and collapsing them would tell the user
      // their resource is gone when it is only hidden from them.
      return "forbidden";
    case "error":
      return "unknown";
    case "ok":
      if (!isSupportedPinSchema(stored.schemaVersion)) {
        return "unknown";
      }
      if (!stored.uid || !outcome.liveUid) {
        return "unknown";
      }
      return stored.uid === outcome.liveUid ? "ok" : "replaced";
  }
}
