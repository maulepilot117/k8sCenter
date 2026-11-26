/**
 * Rubrik provider status store.
 *
 * Holds the answer to GET /v1/rubrik/status for the selected cluster. The
 * provider is off by default (rubrik.enabled), and while it is off the backend
 * mounts no Rubrik route, so the status endpoint answers 404. Only a 404 means
 * "disabled". Any other failure is "error": pages show that the status could
 * not be loaded, never that Rubrik is not enabled.
 *
 * Nothing is fetched until a consumer calls `ensureRubrikStatus()` from an
 * effect, so the server render and pages that never show Rubrik make no
 * request. After that the store reads the status once, and again only when
 * the selected cluster changes (the cluster's Rubrik state is per cluster) or
 * a page asks for a retry.
 *
 * `rubrikNavVisible` drives nav entries marked `requires: "rubrik"`. It
 * follows the last answer that said enabled or disabled, so a failed read
 * keeps the nav as it was, and a failure before any answer keeps it hidden.
 *
 * Module-level signals are process-global. They are never written on the
 * server (no consumer effect runs there), so no state leaks across requests.
 */

import { ApiError, api } from "@/lib/api.ts";
import type { RubrikStatus } from "@/lib/rubrik-types.ts";
import { clusterEpoch, selectedCluster } from "@/src/lib/cluster.ts";
import { batch, computed, effect, signal } from "@/src/lib/signals.ts";

const STATUS_URL = "/v1/rubrik/status";

export type RubrikStatusState =
  | { kind: "loading" }
  | { kind: "disabled" }
  | { kind: "enabled"; status: RubrikStatus }
  | { kind: "error"; httpStatus: number };

/** The current answer for the selected cluster. */
export const rubrikStatus = signal<RubrikStatusState>({ kind: "loading" });

/**
 * Whether the provider was on at the last answer that said so either way:
 * true (status read), false (404) or null (no such answer yet).
 */
const lastKnownEnabled = signal<boolean | null>(null);

/** True when nav entries marked `requires: "rubrik"` may show. */
export const rubrikNavVisible = computed(() => lastKnownEnabled.value === true);

let started = false;
let disposer: (() => void) | null = null;
let ticket = 0;

/**
 * One status read for `cluster`, applied to the store unless a newer read
 * started meanwhile. Never rejects.
 */
export async function loadRubrikStatus(cluster: string): Promise<void> {
  const mine = ++ticket;
  rubrikStatus.value = { kind: "loading" };
  try {
    const res = await api<RubrikStatus>(STATUS_URL, {
      method: "GET",
      clusterId: cluster,
    });
    if (mine !== ticket) return;
    batch(() => {
      rubrikStatus.value = { kind: "enabled", status: res.data };
      lastKnownEnabled.value = true;
    });
  } catch (err) {
    if (mine !== ticket) return;
    if (err instanceof ApiError && err.status === 404) {
      batch(() => {
        rubrikStatus.value = { kind: "disabled" };
        lastKnownEnabled.value = false;
      });
      return;
    }
    rubrikStatus.value = {
      kind: "error",
      httpStatus: err instanceof ApiError ? err.status : 0,
    };
  }
}

/**
 * Starts the store on first call: reads the status now and again on every
 * cluster switch. Later calls do nothing. Call it from an effect, never from
 * a render.
 */
export function ensureRubrikStatus(): void {
  if (started) return;
  started = true;
  disposer = effect(() => {
    const cluster = selectedCluster.value;
    // Subscribe to the epoch too: a switch away and back is still a switch.
    void clusterEpoch.value;
    void loadRubrikStatus(cluster);
  });
}

/** Reads the status again for the selected cluster (a page's retry). */
export function reloadRubrikStatus(): Promise<void> {
  return loadRubrikStatus(selectedCluster.peek());
}

/** Test-only: return the store to its initial state. */
export function resetRubrikStatusForTest(): void {
  disposer?.();
  disposer = null;
  started = false;
  ticket++;
  batch(() => {
    rubrikStatus.value = { kind: "loading" };
    lastKnownEnabled.value = null;
  });
}
