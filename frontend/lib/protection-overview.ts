/**
 * Protection overview (U15, R20–R22, KTD14): the pure merge behind the
 * per-namespace table of Velero and Rubrik protection.
 *
 * The rows are the namespaces the user can list, so a namespace no provider
 * protects still gets one. Each provider's cell is computed from that
 * provider's own API answer, so its access rules apply unchanged (R21):
 * Velero from GET /v1/velero/backups, Rubrik from GET /v1/rubrik/namespaces.
 * Nothing here decides what a user may see; it only reads what each API
 * returned.
 */

import type {
  RubrikFreshness,
  RubrikNamespaceList,
} from "@/lib/rubrik-types.ts";
import { type Backup, getPhaseCategory } from "@/lib/velero-types.ts";

// ---------------------------------------------------------------------------
// Inputs: what each provider's fetch produced

export type VeleroInput =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "not_installed" }
  | { kind: "loaded"; backups: Backup[] };

export type RubrikInput =
  /** The provider is off (status 404): the overview has no Rubrik column. */
  | { kind: "disabled" }
  | { kind: "loading" }
  | { kind: "error" }
  /**
   * The user may see no Rubrik data here: no read grant anywhere (noAccess)
   * or a remote cluster for a non-admin. Every cell reads as not applicable.
   */
  | { kind: "not_applicable" }
  /** No usable mapping. `setupLink` only for the states admins alone see. */
  | { kind: "not_set_up"; setupLink: boolean }
  /** RSC failed with no earlier answer to show. */
  | { kind: "rsc_failing"; reason?: string; lastSuccessAt?: string }
  | { kind: "loaded"; list: RubrikNamespaceList };

// ---------------------------------------------------------------------------
// Cells

export interface BackupPoint {
  name: string;
  /** When the backup finished, or started when it has no completion time. */
  at: string;
}

export type VeleroCell =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "not_installed" }
  /** No backup covers the namespace (or the user may list none). */
  | { kind: "none" }
  | { kind: "protected"; last: BackupPoint }
  /** The newest finished backup failed; `lastGood` is the newest completed one. */
  | { kind: "failed"; failed: BackupPoint; lastGood?: BackupPoint };

export type RubrikCell =
  | { kind: "loading" }
  | { kind: "error" }
  /**
   * No Rubrik data for this namespace: Rubrik does not cover it for this
   * user, or the user has no grant. The two look the same (R22), so a cell
   * never tells a user without access that Rubrik protects the namespace.
   */
  | { kind: "not_applicable" }
  | { kind: "not_set_up"; setupLink: boolean }
  /** RSC is failing: never shown as unprotected (AE6). */
  | { kind: "rsc_failing"; reason?: string; fetchedAt?: string }
  | { kind: "protected"; lastSnapshot?: string; stale?: RubrikFreshness }
  | { kind: "unprotected" };

export interface OverviewRow {
  namespace: string;
  velero: VeleroCell;
  /** Absent when the Rubrik provider is off. */
  rubrik?: RubrikCell;
}

// ---------------------------------------------------------------------------
// Velero

/** Velero's namespace pattern: `*` any run, `?` one character. */
function globMatch(pattern: string, name: string): boolean {
  if (pattern === "*") return true;
  if (!/[*?]/.test(pattern)) return pattern === name;
  const re = new RegExp(
    `^${pattern
      .split("")
      .map((c) =>
        c === "*"
          ? ".*"
          : c === "?"
            ? "."
            : c.replace(/[.+^${}()|[\]\\]/g, "\\$&"),
      )
      .join("")}$`,
  );
  return re.test(name);
}

/**
 * Whether a backup includes namespace `ns`: an empty include list (or `*`)
 * means every namespace, and an exclude entry always wins.
 */
export function backupCovers(b: Backup, ns: string): boolean {
  const included = b.includedNamespaces ?? [];
  const inList =
    included.length === 0 || included.some((p) => globMatch(p, ns));
  if (!inList) return false;
  return !(b.excludedNamespaces ?? []).some((p) => globMatch(p, ns));
}

/** `at` is empty for a backup Velero gave no start or completion time. */
function pointOf(b: Backup): BackupPoint {
  return { name: b.name, at: b.completionTime ?? b.startTime ?? "" };
}

function time(t: string | undefined): number {
  const v = t ? Date.parse(t) : Number.NaN;
  return Number.isNaN(v) ? Number.NEGATIVE_INFINITY : v;
}

/**
 * The Velero cell for one namespace: the newest completed backup covering
 * it, or the newest finished one when that failed. A backup still running
 * or being deleted is neither a recovery point nor a failure, so it is left
 * out.
 */
export function veleroCell(ns: string, input: VeleroInput): VeleroCell {
  if (input.kind !== "loaded") return { kind: input.kind };
  let newestFinished: { b: Backup; good: boolean } | null = null;
  let lastGood: Backup | null = null;
  for (const b of input.backups) {
    if (!backupCovers(b, ns)) continue;
    const cat = getPhaseCategory(b.phase ?? "");
    const good = cat === "success";
    const finished = good || cat === "error" || cat === "warning";
    if (!finished) continue;
    const at = time(b.completionTime ?? b.startTime);
    if (
      !newestFinished ||
      at > time(newestFinished.b.completionTime ?? newestFinished.b.startTime)
    ) {
      newestFinished = { b, good };
    }
    if (
      good &&
      (!lastGood || at > time(lastGood.completionTime ?? lastGood.startTime))
    ) {
      lastGood = b;
    }
  }
  if (!newestFinished) return { kind: "none" };
  if (newestFinished.good) {
    return { kind: "protected", last: pointOf(newestFinished.b) };
  }
  const failed = pointOf(newestFinished.b);
  return lastGood
    ? { kind: "failed", failed, lastGood: pointOf(lastGood) }
    : { kind: "failed", failed };
}

// ---------------------------------------------------------------------------
// Rubrik

/** The Rubrik cell for one namespace (R22). */
export function rubrikCell(ns: string, input: RubrikInput): RubrikCell {
  switch (input.kind) {
    case "disabled":
    case "not_applicable":
      return { kind: "not_applicable" };
    case "loading":
    case "error":
      return { kind: input.kind };
    case "not_set_up":
      return { kind: "not_set_up", setupLink: input.setupLink };
    case "rsc_failing":
      return {
        kind: "rsc_failing",
        reason: input.reason,
        fetchedAt: input.lastSuccessAt,
      };
  }
  const { list } = input;
  if (list.noAccess) return { kind: "not_applicable" };
  const row = list.namespaces.find((r) => r.namespace === ns);
  // A namespace the answer leaves out is one the user may not read, or one
  // Rubrik does not know: the same cell either way (AE1).
  if (!row) return { kind: "not_applicable" };
  const fresh = list.freshness;
  if (row.protected) {
    return {
      kind: "protected",
      lastSnapshot: row.lastSnapshot,
      stale: fresh?.stale ? fresh : undefined,
    };
  }
  // Out-of-date data may not know what protects the namespace now (AE6).
  if (fresh?.stale) {
    return {
      kind: "rsc_failing",
      reason: fresh.error,
      fetchedAt: fresh.fetchedAt,
    };
  }
  return { kind: "unprotected" };
}

// ---------------------------------------------------------------------------
// Rows

/** One row per listed namespace, sorted, with each provider's cell. */
export function overviewRows(
  namespaces: string[],
  velero: VeleroInput,
  rubrik: RubrikInput,
): OverviewRow[] {
  return [...new Set(namespaces)]
    .sort((a, b) => a.localeCompare(b))
    .map((ns) => ({
      namespace: ns,
      velero: veleroCell(ns, velero),
      ...(rubrik.kind === "disabled" ? {} : { rubrik: rubrikCell(ns, rubrik) }),
    }));
}
