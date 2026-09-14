/**
 * Active-cluster state and the switch protocol every cluster-aware consumer
 * builds on (U11a).
 *
 * Client-only in effect — the localStorage side effects are gated behind
 * IS_BROWSER, and the pure helpers below are exported so they can be tested
 * without a DOM.
 *
 * Three signals, not one, because a cluster switch has to be observable as an
 * atomic event rather than as "the id string changed":
 *
 *   - `selectedCluster`           which cluster requests address
 *   - `selectedClusterGeneration` which *registration* of that cluster it is
 *   - `clusterEpoch`              a monotonic counter, one tick per switch
 *
 * The generation identifies a *registration*, not a credential vintage. It is
 * the backend's `TargetSchema.Generation` (D2): the literal "local" for the
 * local cluster, and the cluster record's `createdAt` for a remote one.
 * Because `created_at` is written once and never updated, it changes only when
 * a row is replaced — a delete-and-re-register cycle, which also mints a fresh
 * random 128-bit id. In-place credential rotation does NOT change it; the
 * backend invalidates that case through `EvictCluster` instead. Consumers that
 * cache per-cluster answers (U11b's capability cache) key on the pair so a
 * re-registration cannot be served the previous registration's answers.
 *
 * The epoch exists because id and generation are two signals and a consumer
 * needs one thing to compare against. A request captures the epoch at issue
 * time via `currentTarget()`; when the response lands, an epoch that no longer
 * matches `clusterEpoch.value` means the operator has moved on and the
 * response describes a cluster that is no longer on screen. That is the only
 * reliable way to discard a late reply — the id alone cannot do it, because
 * switching A then B then A returns the id to its original value while every
 * in-flight A-response is still stale.
 *
 * **`clusterEpoch` and `currentTarget()` have no in-product consumer yet.**
 * They are the contract U11b's YAML pin/gate UI is built against. Today a
 * switch is a hard boundary (`ClusterSwitcher` reloads the page), so nothing
 * needs to discard a late reply; when U11b lands the pinned-apply flow, it
 * becomes the first reader. Do not describe epoch-based staleness rejection as
 * something the product currently enforces.
 */
import { batch, effect, signal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

/**
 * The local cluster's id. Mirrors `k8s.LocalClusterID` in the backend; an
 * empty or missing X-Cluster-ID resolves to the same target there
 * (`IsLocalClusterID`), but we always send the explicit literal.
 */
export const LOCAL_CLUSTER_ID = "local";

/**
 * The local cluster's generation. The backend hands back the literal "local"
 * for the local target rather than a timestamp (D2) — it has no cluster record
 * whose `created_at` could serve as one.
 */
export const LOCAL_GENERATION = "local";

/**
 * Generation for a restored cluster whose real generation is not known yet —
 * a selection persisted by a build that stored only the id.
 *
 * It must never collide with a real generation, and it does not: a real one is
 * either the literal "local" or an RFC3339 timestamp. A cache keyed on the
 * pair therefore *misses* on this value instead of wrongly hitting an entry
 * that belongs to some other registration. The switcher replaces it with the
 * authoritative generation as soon as the cluster list resolves.
 */
export const UNKNOWN_GENERATION = "unknown";

/**
 * Storage key holding the JSON-encoded (clusterId, generation) pair.
 *
 * One key, not two, because the pair must be written atomically: two
 * `setItem` calls can be interrupted — storage fills up, the accessor throws —
 * leaving an id beside a stale or absent generation. A half-written pair is
 * worse than none, so there is nothing to half-write.
 */
const TARGET_KEY = "k8scenter.clusterTarget";

/**
 * Pre-U11a key, holding the bare cluster id. Read-only: it is migrated on the
 * next successful write and never written again.
 */
const LEGACY_CLUSTER_KEY = "k8scenter.selectedCluster";

/**
 * An immutable (id, generation, epoch) triple captured at a single instant.
 *
 * This is what "pinning" means everywhere downstream: a request that carries a
 * ClusterTarget is addressed to the cluster the operator was looking at when
 * they issued it, and stays addressed there no matter what the live signals do
 * afterwards.
 */
export interface ClusterTarget {
  readonly clusterId: string;
  readonly generation: string;
  readonly epoch: number;
}

/**
 * Reads the persisted (cluster, generation) pair, falling back to the local
 * cluster for anything missing or unusable.
 *
 * Takes the storage explicitly rather than reaching for `localStorage` so the
 * round-trip is testable off-DOM. A storage that throws on access — Safari
 * private browsing, a blocked-cookies profile — yields the local cluster
 * instead of taking the app down at import time.
 *
 * A legacy id with no generation is restored under `UNKNOWN_GENERATION`, not
 * discarded. Discarding it silently moved the operator to a different cluster
 * on upgrade, which is the retargeting this whole module exists to prevent;
 * the sentinel keeps the selection while still forcing a cache miss.
 */
export function readPersistedTarget(
  storage: Pick<Storage, "getItem"> | null | undefined,
): { clusterId: string; generation: string } {
  const fallback = {
    clusterId: LOCAL_CLUSTER_ID,
    generation: LOCAL_GENERATION,
  };
  if (!storage) return fallback;
  try {
    const raw = storage.getItem(TARGET_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as unknown;
      if (parsed && typeof parsed === "object") {
        const { clusterId, generation } = parsed as Record<string, unknown>;
        if (typeof clusterId === "string" && clusterId) {
          if (clusterId === LOCAL_CLUSTER_ID) return fallback;
          return {
            clusterId,
            generation: typeof generation === "string" && generation
              ? generation
              : UNKNOWN_GENERATION,
          };
        }
      }
      return fallback;
    }

    const legacy = storage.getItem(LEGACY_CLUSTER_KEY);
    if (!legacy || legacy === LOCAL_CLUSTER_ID) return fallback;
    return { clusterId: legacy, generation: UNKNOWN_GENERATION };
  } catch {
    // Storage unavailable, or a hand-edited value that is not valid JSON.
    return fallback;
  }
}

/**
 * Writes the (cluster, generation) pair so a reload restores the same target.
 * Silently does nothing when storage is unavailable or full — a failure to
 * remember the selection must not break the selection itself.
 */
export function persistTarget(
  storage: Pick<Storage, "setItem"> | null | undefined,
  clusterId: string,
  generation: string,
): void {
  if (!storage) return;
  try {
    storage.setItem(TARGET_KEY, JSON.stringify({ clusterId, generation }));
  } catch {
    // Storage disabled or over quota — selection still works for this session.
  }
}

function browserStorage(): Storage | null {
  if (!IS_BROWSER) return null;
  // Reaching for `localStorage` can throw outright — Safari private browsing
  // and blocked-site-data profiles reject the accessor rather than handing
  // back an empty store — and this runs at module import, where a throw takes
  // the whole island down.
  let store: Storage | null = null;
  try {
    store = localStorage;
  } catch {
    store = null;
  }
  return store;
}

const restored = readPersistedTarget(browserStorage());

/**
 * Currently selected cluster ID. Every API call sends this as X-Cluster-ID
 * unless the caller pinned an explicit one.
 *
 * Read freely; write only through `switchCluster`, which keeps it consistent
 * with the generation and the epoch.
 */
export const selectedCluster = signal(restored.clusterId);

/** Generation of the currently selected cluster, as reported by the API. */
export const selectedClusterGeneration = signal(restored.generation);

/**
 * Monotonic counter bumped on every switch. Consumers compare a captured value
 * against the current one to discard responses from a prior target.
 */
export const clusterEpoch = signal(0);

// Persist selection changes so a reload restores the same target.
if (IS_BROWSER) {
  effect(() => {
    persistTarget(
      browserStorage(),
      selectedCluster.value,
      selectedClusterGeneration.value,
    );
  });
}

/**
 * The only sanctioned way to change the active cluster.
 *
 * Writes the id and the generation and then bumps the epoch, all inside a
 * `batch` so subscribers are notified once, after all three have settled.
 * Without the batch a subscriber woken by the id write would observe the new
 * id beside the *previous* generation and epoch — the torn read that every pin
 * downstream is built to prevent. The epoch is still incremented last so the
 * ordering invariant survives even if the batch is ever removed.
 *
 * Re-selecting the cluster that is already active is a no-op: it must not bump
 * the epoch, because the epoch means "the operator moved somewhere else" and a
 * spurious tick would invalidate live caches and mark valid pins stale.
 */
export function switchCluster(id: string, generation: string): void {
  // Normalize the pair as a unit. Defaulting the two independently let a
  // remote id be paired with the "local" generation — a pair describing no
  // real registration, which then persisted and survived the reload guard.
  const clusterId = id || LOCAL_CLUSTER_ID;
  const gen = clusterId === LOCAL_CLUSTER_ID
    ? LOCAL_GENERATION
    : (generation || UNKNOWN_GENERATION);

  if (
    clusterId === selectedCluster.peek() &&
    gen === selectedClusterGeneration.peek()
  ) {
    return;
  }

  batch(() => {
    selectedCluster.value = clusterId;
    selectedClusterGeneration.value = gen;
    clusterEpoch.value = clusterEpoch.peek() + 1;
  });
}

/**
 * Immutable snapshot of the current target, captured at request-issue time.
 *
 * Reads the signals through `.value`, so calling this inside an effect or a
 * component subscribes that consumer to cluster switches — the useful default
 * for anything rendering the target. Callers in event handlers (the intended
 * use) are outside any tracking context and get a plain non-reactive read.
 *
 * The returned object is frozen and never updated in place: a pin held across
 * an await still describes the cluster it was taken from.
 */
export function currentTarget(): ClusterTarget {
  return Object.freeze({
    clusterId: selectedCluster.value,
    generation: selectedClusterGeneration.value,
    epoch: clusterEpoch.value,
  });
}
