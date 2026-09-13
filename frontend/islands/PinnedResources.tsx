import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { selectedCluster } from "@/lib/cluster.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import {
  loadPins,
  pinsForActiveCluster,
  pinsLoaded,
  pinsUnavailable,
  removePin,
} from "@/lib/pin-store.ts";
import type { PinRecord } from "@/lib/preferences.ts";

interface PinnedResourcesProps {
  /** Active route, used to mark the current pin. Mirrors SecondaryNavProps. */
  currentPath: string;
}

const HEADER_STYLE = {
  fontSize: "11px",
  fontWeight: 600,
  textTransform: "uppercase" as const,
  letterSpacing: "0.07em",
  color: "var(--text-muted)",
  padding: "0 9px 7px",
};

/**
 * The user's pinned resources, as a group in the secondary navigation.
 *
 * This list shows STORED IDENTITY ONLY. It does not fetch the objects to see
 * whether they still exist, whether the user can still read them, or whether
 * the uid still matches — that classification happens on the detail page,
 * where the object is being fetched anyway. Resolving them here would mean an
 * N+1 fan-out on every navigation, in the sidebar, on every page. If a pin is
 * stale the user finds out when they open it, which is one click away.
 */
export default function PinnedResources({ currentPath }: PinnedResourcesProps) {
  const busyId = useSignal<string | null>(null);
  const removeError = useSignal<string | null>(null);
  const cluster = selectedCluster.value;

  useEffect(() => {
    if (!IS_BROWSER) return;
    const ac = new AbortController();
    loadPins(ac.signal);
    return () => ac.abort();
  }, [cluster]);

  const mine = pinsForActiveCluster();

  const unpin = async (record: PinRecord) => {
    busyId.value = record.id;
    removeError.value = null;
    try {
      await removePin(record.id);
    } catch {
      removeError.value = `Could not unpin ${record.config.name}.`;
    } finally {
      busyId.value = null;
    }
  };

  // Three different situations, three different renderings. Collapsing any
  // pair of them would tell the user something untrue (R3).
  const unavailable = pinsUnavailable.value;
  const loading = !pinsLoaded.value && !unavailable;

  if (!unavailable && pinsLoaded.value && mine.length === 0) {
    // Nothing pinned on this cluster: show the header alone so the section
    // exists and reads as empty, rather than vanishing as if unsupported.
    return (
      <div style={{ marginTop: "14px" }} data-testid="pinned-resources">
        <div style={HEADER_STYLE}>Pinned</div>
        <div
          data-testid="pinned-empty"
          style={{
            fontSize: "12px",
            color: "var(--text-muted)",
            padding: "0 9px 4px",
          }}
        >
          Nothing pinned yet.
        </div>
      </div>
    );
  }

  return (
    <div style={{ marginTop: "14px" }} data-testid="pinned-resources">
      <div style={HEADER_STYLE}>Pinned</div>

      {loading && (
        <div
          style={{
            fontSize: "12px",
            color: "var(--text-muted)",
            padding: "0 9px 4px",
          }}
        >
          Loading...
        </div>
      )}

      {unavailable && (
        <div
          data-testid="pinned-unavailable"
          style={{
            fontSize: "12px",
            color: "var(--warning)",
            padding: "0 9px 4px",
          }}
        >
          {unavailable === "database_unavailable"
            ? "Pins need a database, and this deployment has none."
            : "Your pins could not be loaded."}
        </div>
      )}

      {mine.map((record) => {
        const { resourceKind, namespace, name, displayKind } = record.config;
        const href = resourceHref(resourceKind, namespace, name);
        const active = href !== null && currentPath === href;
        const label = displayKind || resourceKind;

        const row = (
          <>
            <span
              style={{
                width: "6px",
                height: "6px",
                borderRadius: "50%",
                flexShrink: 0,
                background: href === null
                  ? "var(--border-primary)"
                  : "var(--accent)",
              }}
            />
            <span
              style={{
                fontSize: "13px",
                fontWeight: 500,
                flex: 1,
                minWidth: 0,
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
              title={namespace
                ? `${label} ${namespace}/${name}`
                : `${label} ${name}`}
            >
              {name}
            </span>
            <button
              type="button"
              data-testid="unpin"
              aria-label={`Unpin ${label} ${name}`}
              disabled={busyId.value === record.id}
              onClick={(e) => {
                // The whole row is an anchor, so a click here would navigate
                // before the unpin request is even sent.
                e.preventDefault();
                e.stopPropagation();
                unpin(record);
              }}
              style={{
                flexShrink: 0,
                padding: "0 4px",
                fontSize: "13px",
                lineHeight: 1,
                background: "transparent",
                border: "none",
                color: "var(--text-muted)",
                cursor: "pointer",
              }}
            >
              x
            </button>
          </>
        );

        const rowStyle = {
          display: "flex",
          alignItems: "center",
          gap: "10px",
          padding: "8px 9px",
          borderRadius: "8px",
          textDecoration: "none",
          marginBottom: "1px",
          color: "var(--text-primary)",
          background: active
            ? "color-mix(in srgb, var(--accent) 12%, transparent)"
            : "transparent",
        };

        // An unroutable kind gets a row that says so rather than a dead link.
        // resourceHref returns null for kinds with no detail page, and an
        // anchor to "#" would look navigable and do nothing.
        return href === null
          ? (
            <div
              key={record.id}
              data-testid="pinned-row-unsupported"
              data-pin-name={name}
              title={`${label} ${name} — this build has no detail page for ${resourceKind}`}
              style={{ ...rowStyle, cursor: "not-allowed" }}
            >
              {row}
            </div>
          )
          : (
            <a
              key={record.id}
              href={href}
              data-testid="pinned-row"
              data-pin-name={name}
              style={rowStyle}
            >
              {row}
            </a>
          );
      })}

      {removeError.value && (
        <div
          data-testid="pinned-remove-error"
          style={{
            fontSize: "12px",
            color: "var(--error)",
            padding: "2px 9px 4px",
          }}
        >
          {removeError.value}
        </div>
      )}
    </div>
  );
}
