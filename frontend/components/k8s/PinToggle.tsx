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
  /**
   * True once the page knows this object is gone -- today, when a DELETED
   * event arrives over the WebSocket while the page is open.
   *
   * Required, not optional: ResourceDetail keeps `resource.value` populated
   * after a delete so the page can still render the object it was showing,
   * which means the uid alone cannot distinguish a live object from a deleted
   * one. Without this the control would keep reporting "Pinned" for an object
   * that no longer exists.
   */
  deleted: boolean;
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
 * The control renders only once the detail fetch produced an object, so a
 * cold-load "forbidden" or "not found" never reaches it — ResourceDetail
 * renders its own banner and no action area at all, and that page-level
 * report is more precise than anything this button could say. Do not
 * synthesize those two states here from a failed fetch.
 *
 * A delete that arrives AFTER the object loaded is the exception, and the
 * `deleted` prop carries it: the page deliberately keeps showing the object
 * it had, so without that signal this control would keep reporting "Pinned"
 * for a resource that no longer exists.
 */
export function PinToggle(
  { resourceKind, displayKind, namespace, name, uid, deleted }: PinToggleProps,
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
  // Every read of the shared pin-store is gated on IS_BROWSER together, not
  // one of three: those signals are a process-global singleton on the Deno
  // server, so a partial guard would let one request's state colour another
  // request's server-rendered output.
  const record = IS_BROWSER
    ? pinsForActiveCluster().find((p) => pinDedupKey(p.config) === key)
    : undefined;
  const unavailable = IS_BROWSER ? pinsUnavailable.value : undefined;
  const loaded = IS_BROWSER ? pinsLoaded.value : false;

  const loading = uid === undefined || (!loaded && !unavailable);
  // A deleted target is a "notFound" lookup even though the page still holds
  // the object it was showing, so the record classifies as missing rather than
  // matching its own stale uid.
  const live = record
    ? classifyPin(record.config, {
      status: deleted ? "notFound" : "ok",
      liveUid: uid,
    })
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
      case "invalid_name":
      case "identity_too_long":
        // The server rejected what we sent. Saying "could not reach" here
        // would send the user looking for an outage that is not happening.
        return "This resource's name cannot be stored as a pin.";
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
  //
  // The two steps are caught separately on purpose. Sharing one try would make
  // a failed delete skip the create, so a user who asked to re-pin would end
  // up with no pin at all -- and the delete's most likely failure is a 404
  // from another tab having removed the same record, which is not a failure of
  // this operation at all.
  const repin = async () => {
    if (!record) return;
    busy.value = true;
    failure.value = null;
    try {
      try {
        await removePin(record.id);
      } catch (err) {
        // Already gone is the state this step wanted. Anything else leaves the
        // old record in place, so stop rather than creating a second one.
        if ((err as ApiError | undefined)?.status !== 404) throw err;
      }
      await addPin(recordName, config);
    } catch (err) {
      if (preferenceReason(err) === "already_pinned") {
        // Another tab re-pinned the same object first. The end state is the
        // one the user asked for, so reconcile instead of reporting a failure
        // next to a control that correctly reads "Pinned".
        await loadPins();
      } else {
        failure.value =
          `Could not re-pin, and the old pin may already be gone. ${
            describe(err)
          }`;
        await loadPins();
      }
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
    // Pinning an object the page already knows is gone would store a record
    // that is stale the moment it is written.
    action = deleted ? undefined : pin;
    ariaLabel = deleted
      ? `${displayKind} ${name} was deleted and cannot be pinned`
      : `Pin ${displayKind} ${name}`;
    hint = deleted ? "This resource was deleted." : undefined;
  } else if (live === "missing") {
    state = "missing";
    text = "Pinned (target deleted)";
    action = unpin;
    ariaLabel = `Unpin ${displayKind} ${name}`;
    hint =
      "This resource was deleted while you were viewing it. The pin still points at it.";
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
