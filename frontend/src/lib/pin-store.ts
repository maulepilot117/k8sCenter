/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 *
 * It holds module-level signals, which in Deno are a process-global singleton:
 * imported during SSR, one request's pins would be visible to the next one's
 * render. It also imports lib/preferences.ts, whose own banner says the same
 * thing about the access token behind it. Import this from islands only.
 *
 * Deliberately NOT localStorage-backed. Pins are server state — the whole
 * point is that they follow the user to another browser — and any localStorage
 * key here would additionally be captured into Playwright's storageState and
 * leak across the entire E2E suite.
 *
 * Shared so SecondaryNav and the resource detail page read one list instead of
 * fetching their own copies and disagreeing about what is pinned.
 */
import { signal } from "@preact/signals";
import type { PinConfig } from "@/lib/preference-types.ts";
import {
  type PinRecord,
  type PreferenceReason,
  preferenceReason,
  preferencesApi,
} from "@/lib/preferences.ts";
import { selectedCluster } from "@/src/lib/cluster.ts";

/** Every pin the user owns, across all clusters. Filter before rendering. */
export const pins = signal<PinRecord[]>([]);

/** True once a load has succeeded at least once. */
export const pinsLoaded = signal(false);

/**
 * Why the list is unusable, when it is. Kept separate from `pins` so a failed
 * load never renders as "you have no pins" — an absent observation must not
 * look like an empty one (R3).
 */
export const pinsUnavailable = signal<PreferenceReason | undefined>(undefined);

let inFlight: AbortController | null = null;

/**
 * Load pins for the current user.
 *
 * On failure `pins` is left exactly as it was: a transient error must not
 * blank a list the user was looking at a moment ago. The caller renders the
 * unavailable state from `pinsUnavailable` instead.
 */
export async function loadPins(signal?: AbortSignal): Promise<void> {
  // Cancel any previous load so a slow response for the old cluster cannot
  // land late and present another cluster's pins as this one's.
  inFlight?.abort();
  const ac = new AbortController();
  inFlight = ac;

  const onAbort = () => ac.abort();
  signal?.addEventListener("abort", onAbort, { once: true });

  try {
    const records = await preferencesApi.listPins(ac.signal);
    if (ac.signal.aborted) return;
    pins.value = records;
    pinsLoaded.value = true;
    pinsUnavailable.value = undefined;
  } catch (err) {
    if (
      ac.signal.aborted ||
      (err instanceof DOMException && err.name === "AbortError")
    ) {
      return;
    }
    pinsUnavailable.value = preferenceReason(err);
    // pins.value is intentionally untouched.
  } finally {
    signal?.removeEventListener("abort", onAbort);
    if (inFlight === ac) inFlight = null;
  }
}

/**
 * Pin a resource. The new record is appended locally so the nav updates
 * without a second round trip; the server remains the source of truth on the
 * next load.
 */
export async function addPin(
  name: string,
  config: PinConfig,
): Promise<PinRecord> {
  const record = await preferencesApi.createPin(name, config);
  pins.value = [...pins.value, record];
  return record;
}

/** Remove a pin. Local state follows only after the server confirms. */
export async function removePin(id: string): Promise<void> {
  await preferencesApi.deletePin(id);
  pins.value = pins.value.filter((p) => p.id !== id);
}

/**
 * Pins addressed to the cluster currently selected.
 *
 * A pin names one object in one cluster, so a pin from another cluster cannot
 * be opened from here — unlike a saved view, which describes a scope that a
 * different cluster could in principle satisfy. Other clusters' pins are
 * therefore filtered out rather than shown disabled.
 */
export function pinsForActiveCluster(): PinRecord[] {
  return pins.value.filter((p) => p.clusterId === selectedCluster.value);
}
