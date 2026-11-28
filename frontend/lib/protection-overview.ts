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
  RubrikCompliance,
  RubrikFreshness,
  RubrikNamespaceList,
  RubrikNamespaceRow,
  RubrikObject,
} from "@/lib/rubrik-types.ts";
import { type Backup, getPhaseCategory } from "@/lib/velero-types.ts";

// ---------------------------------------------------------------------------
// Inputs: what each provider's fetch produced

export type VeleroInput =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "not_installed" }
  /** The user may not list Velero backups (the list's metadata.accessDenied). */
  | { kind: "denied" }
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
  /**
   * RSC failed with no earlier answer to show. `lastFetchedAt` is the fetch
   * time of the last answer this page loaded for the cluster, if any.
   */
  | { kind: "rsc_failing"; reason?: string; lastFetchedAt?: string }
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
  /** The user may not list Velero backups. */
  | { kind: "denied" }
  /** Backups were listed, and none covers the namespace. */
  | { kind: "none" }
  /**
   * The backup list came back empty. A denial says so (`denied`), but a
   * backend older than that flag answers a denial with the same empty list,
   * so the cell still does not claim the namespace has none.
   */
  | { kind: "none_visible" }
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
  | {
      kind: "protected";
      lastSnapshot?: string;
      /** The SLA domains of the objects protecting it, without repeats. */
      slas: string[];
      compliance: RubrikCompliance;
      stale?: RubrikFreshness;
    }
  | { kind: "unprotected" };

export interface OverviewRow {
  namespace: string;
  velero: VeleroCell;
  /** Absent when the Rubrik provider is off. */
  rubrik?: RubrikCell;
}

// ---------------------------------------------------------------------------
// Velero

/**
 * Whether a backup includes namespace `ns`, by the rule Backup Assurance
 * applies on the server (velero/assurance.go coversNamespace), so the two
 * Backup pages never disagree: an exclude entry naming `ns` wins, then an
 * empty or `*` include list covers every namespace, else `ns` must be
 * named. Other patterns are not expanded.
 */
export function backupCovers(b: Backup, ns: string): boolean {
  if ((b.excludedNamespaces ?? []).includes(ns)) return false;
  const included = b.includedNamespaces ?? [];
  return (
    included.length === 0 || included.includes("*") || included.includes(ns)
  );
}

/**
 * Velero phases that are still running although their name says a part
 * failed: the backup is waiting for plugin operations or finalizing.
 */
function stillRunning(phase: string): boolean {
  const p = phase.toLowerCase();
  return (
    p.startsWith("waitingforpluginoperations") || p.startsWith("finalizing")
  );
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
  if (input.backups.length === 0) return { kind: "none_visible" };
  let newestFinished: { b: Backup; good: boolean } | null = null;
  let lastGood: Backup | null = null;
  for (const b of input.backups) {
    if (!backupCovers(b, ns)) continue;
    const phase = b.phase ?? "";
    if (stillRunning(phase)) continue;
    // Completed is good; Failed, PartiallyFailed and FailedValidation all
    // count as a failure (getPhaseCategory calls each "error").
    const cat = getPhaseCategory(phase);
    const good = cat === "success";
    if (!good && cat !== "error") continue;
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
        fetchedAt: input.lastFetchedAt,
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
      slas: slaNames(row, list.objects),
      compliance: row.compliance,
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

/**
 * The effective SLA domain of each object protecting the namespace, in the
 * answer's order and without repeats. An object with no SLA reads "No SLA",
 * as on the Rubrik pages.
 */
function slaNames(row: RubrikNamespaceRow, objects: RubrikObject[]): string[] {
  const ids = new Set(row.objectIds);
  const names: string[] = [];
  for (const o of objects) {
    if (!ids.has(o.id)) continue;
    const name = o.effectiveSla?.name || "No SLA";
    if (!names.includes(name)) names.push(name);
  }
  return names;
}

/**
 * What the snapshot and SLA actions on one namespace need: its row and the
 * objects it names (protecting it, or targets of an action on it). Absent
 * when Rubrik gave no row for the namespace, so no action is offered. The
 * server checks every write again; the row's flags only decide what shows.
 */
export function rubrikActions(
  ns: string,
  input: RubrikInput,
): { namespace: RubrikNamespaceRow; objects: RubrikObject[] } | undefined {
  if (input.kind !== "loaded" || input.list.noAccess) return undefined;
  const row = input.list.namespaces.find((r) => r.namespace === ns);
  if (!row) return undefined;
  const ids = new Set([...row.objectIds, ...row.actionTargets]);
  return {
    namespace: row,
    objects: input.list.objects.filter((o) => ids.has(o.id)),
  };
}

// ---------------------------------------------------------------------------
// Rows

/**
 * The namespaces the providers' own answers name: the Rubrik rows, and the
 * namespaces Velero backups name explicitly. Used when the user may not list
 * namespaces, so they still see the namespaces each provider shows them.
 */
export function providerNamespaces(
  velero: VeleroInput,
  rubrik: RubrikInput,
): string[] {
  const out = new Set<string>();
  if (rubrik.kind === "loaded" && !rubrik.list.noAccess) {
    for (const r of rubrik.list.namespaces) out.add(r.namespace);
  }
  if (velero.kind === "loaded") {
    for (const b of velero.backups) {
      for (const ns of b.includedNamespaces ?? []) {
        if (ns !== "*") out.add(ns);
      }
    }
  }
  return [...out];
}

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
