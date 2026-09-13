import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import {
  addPin,
  loadPins,
  pinsForActiveCluster,
  pinsLoaded,
  pinsUnavailable,
  removePin,
} from "@/lib/pin-store.ts";
import {
  classifyPin,
  MAX_PINS,
  MAX_RECORD_NAME_LEN,
  PIN_SCHEMA_VERSION,
  type PinConfig,
  pinDedupKey,
} from "@/lib/preference-types.ts";
import { preferenceReason } from "@/lib/preferences.ts";
import type { ApiError } from "@/lib/api.ts";

interface PinToggleProps {
  /** Adapter slug, e.g. "deployments". */
  resourceKind: string;
  /** RESOURCE_API_KINDS[kind] ?? title, e.g. "Deployment". */
  displayKind: string;
  /** "" for cluster-scoped resources. */
  namespace: string;
  name: string;
  /** The live object's uid; undefined until the detail fetch resolves. */
  uid: string | undefined;
}

/**
 * Pin/unpin control for the resource detail header.
 *
 * A plain component rather than an island: ResourceDetail is already an
 * island, so this ships inside that bundle and shares the one pin-store
 * instance the secondary navigation reads. Two islands fetching their own pin
 * lists would eventually disagree about what is pinned.
 *
 * It deliberately does NOT load the pin list itself. PinnedResources, which
 * _layout.tsx renders in the secondary navigation on every authenticated
 * page, owns that one load; a second loader here would abort the nav's
 * request and re-issue an identical one on every detail page. Until that load
 * lands this control reads "loading" and stays disabled, which is the honest
 * answer: it does not yet know whether this resource is pinned, and offering
 * "Pin" would let a user create a duplicate of a pin they already have.
 *
 * This control renders only once the detail fetch produced an object, so the
 * live-lookup outcome it can observe is always "ok" — which is why it shows
 * three of classifyPin's five states (ok, replaced, unknown). The other two
 * belong to the page, not to this button: when the target is gone or the user
 * may no longer read it, ResourceDetail renders its own not-found or
 * forbidden banner instead of the object. Do not synthesize a "missing" pin
 * state here from a failed fetch — the page has already said it, and said it
 * more precisely.
 */
export function PinToggle(
  { resourceKind, displayKind, namespace, name, uid }: PinToggleProps,
) {
  const busy = useSignal(false);
  const failure = useSignal<string | null>(null);

  const config: PinConfig = {
    schemaVersion: PIN_SCHEMA_VERSION,
    resourceKind,
    group: "",
    version: "",
    namespace,
    name,
    uid: uid ?? "",
    displayKind,
  };

  const key = pinDedupKey(config);
  // Active cluster only: the same kind/namespace/name can be pinned on two
  // clusters, and the other cluster's pin says nothing about this object.
  const record = IS_BROWSER
    ? pinsForActiveCluster().find((p) => pinDedupKey(p.config) === key)
    : undefined;

  const unavailable = pinsUnavailable.value;
  const loading = uid === undefined || (!pinsLoaded.value && !unavailable);
  const live = record
    ? classifyPin(record.config, { status: "ok", liveUid: uid })
    : undefined;

  const label = namespace
    ? `${displayKind} ${namespace}/${name}`
    : `${displayKind} ${name}`;
  // Character count, not UTF-16 length: the server measures runes.
  const recordName = [...label].slice(0, MAX_RECORD_NAME_LEN).join("");

  const describe = (e: unknown): string => {
    switch (preferenceReason(e)) {
      case "limit_reached":
        return `You have reached the ${MAX_PINS}-pin limit. Unpin something first.`;
      case "database_unavailable":
        return "Pins need a database, and this deployment has none.";
      case "invalid_config":
      case "unknown_resource_kind":
      case "unsupported_schema_version":
        return "This build cannot pin this resource.";
      default:
        return "Could not reach the preference service.";
    }
  };

  const pin = async () => {
    busy.value = true;
    failure.value = null;
    try {
      await addPin(recordName, config);
    } catch (err) {
      if (preferenceReason(err) === "already_pinned") {
        // Another tab got there first. That is the outcome the user asked
        // for, so reconcile with the server instead of reporting an error.
        await loadPins();
      } else {
        failure.value = describe(err);
      }
    } finally {
      busy.value = false;
    }
  };

  const unpin = async () => {
    if (!record) return;
    busy.value = true;
    failure.value = null;
    try {
      await removePin(record.id);
    } catch (err) {
      // 404 means another tab already removed it — the same outcome, so
      // reconcile quietly. Anything else failed, and the user has to know,
      // because the pin they just tried to remove is still there.
      const status = (err as ApiError | undefined)?.status;
      await loadPins();
      if (status !== 404) failure.value = describe(err);
    } finally {
      busy.value = false;
    }
  };

  // Re-pinning a replaced object is a delete plus a create, not an update: a
  // pin has no PUT, and this is a different object than the one pinned.
  const repin = async () => {
    if (!record) return;
    busy.value = true;
    failure.value = null;
    try {
      await removePin(record.id);
      await addPin(recordName, config);
    } catch (err) {
      // The delete may already have landed, so say so rather than leaving the
      // user to discover the pin is gone from the navigation.
      failure.value = `Could not re-pin, and the old pin may already be gone. ${
        describe(err)
      }`;
      await loadPins();
    } finally {
      busy.value = false;
    }
  };

  const buttonStyle = (accent: boolean) => ({
    padding: "6px 12px",
    borderRadius: "9px",
    fontSize: "12px",
    fontWeight: 600,
    fontFamily: "inherit",
    border: "1px solid var(--border-primary)",
    background: "transparent",
    color: accent ? "var(--accent)" : "var(--text-muted)",
    display: "inline-flex",
    alignItems: "center",
    gap: "5px",
  });

  // Each situation gets its own label. "Pinned" must never stand in for a pin
  // this build could not verify, and an unreachable preference service must
  // never render as an unpinned resource (R3).
  let state: string;
  let text: string;
  let action: (() => void) | undefined;
  let ariaLabel: string;
  let hint: string | undefined;

  if (unavailable) {
    state = "unavailable";
    text = "Pin unavailable";
    ariaLabel = `Pinning is unavailable for ${displayKind} ${name}`;
    hint = unavailable === "database_unavailable"
      ? "Pins need a database, and this deployment has none."
      : "Your pins could not be loaded, so this resource's pin state is unknown.";
  } else if (loading) {
    state = "loading";
    text = "Pin";
    ariaLabel = `Pin ${displayKind} ${name} (loading)`;
    hint = "Waiting to find out whether this resource is pinned.";
  } else if (!record) {
    state = "unpinned";
    text = "Pin";
    action = pin;
    ariaLabel = `Pin ${displayKind} ${name}`;
  } else if (live === "replaced") {
    state = "replaced";
    text = "Pinned (replaced)";
    action = unpin;
    ariaLabel = `Unpin ${displayKind} ${name}`;
    hint =
      `A different ${displayKind} now carries this name. The pin still records the object you pinned.`;
  } else if (live === "unknown") {
    state = "unknown";
    text = "Pinned (unverified)";
    action = unpin;
    ariaLabel = `Unpin ${displayKind} ${name}`;
    hint =
      "This pin was stored without verifiable identity, so it cannot be confirmed to point at this object.";
  } else {
    state = "pinned";
    text = "Pinned";
    action = unpin;
    ariaLabel = `Unpin ${displayKind} ${name}`;
  }

  const disabled = busy.value || action === undefined;

  return (
    <span
      style={{ display: "inline-flex", alignItems: "center", gap: "6px" }}
      data-testid="pin-toggle"
      data-pin-state={state}
    >
      <button
        type="button"
        data-testid="pin-button"
        aria-label={ariaLabel}
        aria-busy={busy.value ? "true" : undefined}
        title={hint}
        disabled={disabled}
        onClick={() => action?.()}
        style={{
          ...buttonStyle(state === "pinned"),
          cursor: disabled ? "not-allowed" : "pointer",
          opacity: disabled ? 0.5 : 1,
        }}
      >
        {text}
      </button>

      {state === "replaced" && (
        <button
          type="button"
          data-testid="pin-repin"
          aria-label={`Re-pin the current ${displayKind} ${name}`}
          disabled={busy.value}
          onClick={repin}
          style={{
            ...buttonStyle(true),
            cursor: busy.value ? "not-allowed" : "pointer",
            opacity: busy.value ? 0.5 : 1,
          }}
        >
          Re-pin this one
        </button>
      )}

      {failure.value && (
        <span
          data-testid="pin-error"
          style={{ fontSize: "12px", color: "var(--error)" }}
        >
          {failure.value}
        </span>
      )}
    </span>
  );
}
