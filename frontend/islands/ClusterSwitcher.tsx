import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import {
  LOCAL_CLUSTER_ID,
  LOCAL_GENERATION,
  selectedCluster,
  selectedClusterGeneration,
  switchCluster,
} from "@/lib/cluster.ts";

/**
 * One row of GET /v1/clusters. A subset of the backend's `ClusterRecord` —
 * only the fields the switcher renders or derives the generation from.
 */
interface ClusterListItem {
  id: string;
  name: string;
  displayName?: string;
  status?: string;
  isLocal?: boolean;
  /** RFC3339. The credential generation for remote clusters (D2). */
  createdAt?: string;
}

/** What the switcher renders: an id, a label, a generation, and a health dot. */
interface ClusterOption {
  id: string;
  label: string;
  generation: string;
  status: string;
}

/**
 * The option the product always has. A deployment with no database answers 503
 * on /v1/clusters and a non-admin answers 403, but both can still use the
 * cluster k8sCenter runs in — so "local" is a constant, not something the list
 * response is allowed to take away.
 */
const LOCAL_OPTION: ClusterOption = {
  id: LOCAL_CLUSTER_ID,
  label: "local",
  generation: LOCAL_GENERATION,
  status: "connected",
};

/**
 * The generation to pin a cluster at. The backend's TargetSchema reports the
 * literal "local" for the local cluster and the cluster record's created_at
 * for a remote one (D2); mirroring that here keeps the client's cache keys
 * describing the same registration the server thinks it is talking about.
 *
 * A remote row with no createdAt (an older backend) falls back to its id,
 * which is a stable-but-useless generation rather than a wrong one: it never
 * collides with another cluster's, and it degrades the cache to "never
 * invalidated by re-registration" instead of "invalidated constantly".
 */
function generationOf(c: ClusterListItem): string {
  if (c.isLocal || c.id === LOCAL_CLUSTER_ID) return LOCAL_GENERATION;
  return c.createdAt || c.id;
}

function toOption(c: ClusterListItem): ClusterOption {
  return {
    id: c.id,
    label: c.displayName || c.name || c.id,
    generation: generationOf(c),
    status: c.status || "unknown",
  };
}

function dotColor(status: string): string {
  switch (status) {
    case "connected":
      return "var(--success)";
    case "degraded":
      return "var(--warning)";
    case "disconnected":
    case "error":
      return "var(--danger)";
    default:
      return "var(--text-muted)";
  }
}

/**
 * Top-bar cluster switcher.
 *
 * This is the UI half of multi-cluster: the backend has routed by
 * X-Cluster-ID since Phase 2, but nothing in the web app ever wrote
 * `selectedCluster`, so every registered remote cluster was unreachable from
 * the browser. Changing the selection goes through `switchCluster` so the id,
 * the generation and the epoch move together.
 */
export default function ClusterSwitcher() {
  const options = useSignal<ClusterOption[]>([LOCAL_OPTION]);
  const open = useSignal(false);
  const activeIndex = useSignal(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;

    (async () => {
      try {
        const res = await apiGet<ClusterListItem[]>("/v1/clusters", {
          // The list itself is not cluster-scoped, but an unpinned request
          // would address whatever cluster is selected — including one that
          // has since been deleted. Ask the local cluster, always.
          clusterId: LOCAL_CLUSTER_ID,
        });
        if (cancelled) return;

        const rows = Array.isArray(res.data) ? res.data : [];
        const mapped = rows.map(toOption);
        // Guarantee a local entry even if the registry somehow lacks one.
        options.value = mapped.some((o) => o.id === LOCAL_CLUSTER_ID)
          ? mapped
          : [LOCAL_OPTION, ...mapped];

        // Reconcile the restored selection against the clusters that actually
        // exist. A cluster deleted while it was the active selection would
        // otherwise be restored from localStorage forever, and every request
        // in the app would address a cluster the registry no longer knows.
        //
        // This also refreshes the generation, which matters for a cluster
        // re-registered under a recycled id: that is a different registration,
        // and the stale generation would let U11b serve the previous one's
        // cached capabilities.
        //
        // Only the success path reconciles. See the catch below.
        const match = options.value.find((o) => o.id === selectedCluster.value);
        switchCluster(
          match?.id ?? LOCAL_OPTION.id,
          match?.generation ?? LOCAL_OPTION.generation,
        );
      } catch {
        // 403 (not an admin) and 503 (no database) are both ordinary states,
        // not errors to show an operator: they mean "you get the local
        // cluster", which is a working product. A transient failure — offline,
        // a 500 — lands here too.
        //
        // Deliberately NOT reconciling here. A failed list is an absence of
        // information, not evidence that the selected cluster is gone, and
        // silently moving the operator to a different cluster on a dropped
        // request is the exact retargeting this unit exists to prevent. The
        // selection stands; the list degrades to what is certain — the local
        // cluster, plus an entry for the current selection so the trigger
        // keeps naming the cluster requests are actually going to.
        if (cancelled) return;
        options.value = selectedCluster.value === LOCAL_CLUSTER_ID
          ? [LOCAL_OPTION]
          : [LOCAL_OPTION, {
            id: selectedCluster.value,
            label: selectedCluster.value,
            generation: selectedClusterGeneration.value,
            status: "unknown",
          }];
      }
    })();

    return () => {
      cancelled = true;
    };
  }, []);

  // Move focus to the listbox once it has actually rendered, so arrow keys
  // drive aria-activedescendant. Scheduling this from the click handler would
  // race Preact's own render flush.
  useEffect(() => {
    if (open.value) listRef.current?.focus();
  }, [open.value]);

  const current = options.value.find((o) => o.id === selectedCluster.value) ??
    LOCAL_OPTION;

  function choose(o: ClusterOption) {
    switchCluster(o.id, o.generation);
    open.value = false;
    buttonRef.current?.focus();
  }

  function openList() {
    const idx = options.value.findIndex((o) => o.id === selectedCluster.value);
    activeIndex.value = idx >= 0 ? idx : 0;
    open.value = true;
  }

  function onButtonKeyDown(e: KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter") {
      e.preventDefault();
      openList();
    }
  }

  function onListKeyDown(e: KeyboardEvent) {
    const items = options.value;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        activeIndex.value = (activeIndex.value + 1) % items.length;
        break;
      case "ArrowUp":
        e.preventDefault();
        activeIndex.value = (activeIndex.value - 1 + items.length) %
          items.length;
        break;
      case "Home":
        e.preventDefault();
        activeIndex.value = 0;
        break;
      case "End":
        e.preventDefault();
        activeIndex.value = items.length - 1;
        break;
      case "Enter":
      case " ":
        e.preventDefault();
        if (items[activeIndex.value]) choose(items[activeIndex.value]);
        break;
      case "Escape":
        e.preventDefault();
        open.value = false;
        buttonRef.current?.focus();
        break;
    }
  }

  return (
    <div
      ref={rootRef}
      style={{ position: "relative" }}
      // Close when focus leaves the switcher entirely. Scoped to the wrapper
      // rather than put on the listbox so that moving focus back to the
      // trigger does not close-then-reopen the list.
      onFocusOut={(e) => {
        const next = e.relatedTarget as Node | null;
        if (next && rootRef.current?.contains(next)) return;
        open.value = false;
      }}
    >
      <button
        ref={buttonRef}
        type="button"
        aria-label={`Active cluster: ${current.label}. Change cluster`}
        aria-haspopup="listbox"
        aria-expanded={open.value}
        aria-controls="cluster-switcher-list"
        onClick={() => (open.value ? (open.value = false) : openList())}
        onKeyDown={onButtonKeyDown}
        style={{
          display: "flex",
          alignItems: "center",
          gap: "6px",
          padding: "4px 10px",
          borderRadius: "6px",
          background: "var(--bg-elevated)",
          border: "1px solid var(--border-subtle)",
          fontSize: "13px",
          color: "var(--text-primary)",
          cursor: "pointer",
        }}
      >
        <span
          style={{
            width: "8px",
            height: "8px",
            borderRadius: "50%",
            background: dotColor(current.status),
            flexShrink: 0,
          }}
        />
        <span style={{ fontWeight: 500 }}>{current.label}</span>
        <span aria-hidden="true" style={{ color: "var(--text-muted)" }}>▾</span>
      </button>

      {open.value && (
        <ul
          ref={listRef}
          id="cluster-switcher-list"
          role="listbox"
          tabIndex={0}
          aria-label="Clusters"
          aria-activedescendant={`cluster-option-${
            options.value[activeIndex.value]?.id ?? ""
          }`}
          onKeyDown={onListKeyDown}
          style={{
            position: "absolute",
            top: "calc(100% + 4px)",
            left: 0,
            minWidth: "180px",
            margin: 0,
            padding: "4px",
            listStyle: "none",
            borderRadius: "8px",
            background: "var(--bg-elevated)",
            border: "1px solid var(--border-subtle)",
            boxShadow: "var(--shadow-lg, 0 8px 24px rgba(0,0,0,0.35))",
            outline: "none",
            zIndex: 50,
          }}
        >
          {options.value.map((o, i) => (
            <li
              key={o.id}
              id={`cluster-option-${o.id}`}
              role="option"
              aria-selected={o.id === selectedCluster.value}
              // Pointer selection must beat the listbox's own blur handler,
              // which would otherwise close the list before the click lands.
              onMouseDown={(e) => {
                e.preventDefault();
                choose(o);
              }}
              onMouseEnter={() => {
                activeIndex.value = i;
              }}
              style={{
                display: "flex",
                alignItems: "center",
                gap: "8px",
                padding: "6px 8px",
                borderRadius: "6px",
                fontSize: "13px",
                color: "var(--text-primary)",
                cursor: "pointer",
                background: i === activeIndex.value
                  ? "var(--bg-hover, rgba(255,255,255,0.06))"
                  : "transparent",
              }}
            >
              <span
                style={{
                  width: "8px",
                  height: "8px",
                  borderRadius: "50%",
                  background: dotColor(o.status),
                  flexShrink: 0,
                }}
              />
              <span style={{ flex: 1 }}>{o.label}</span>
              {o.id === selectedCluster.value && (
                <span aria-hidden="true" style={{ color: "var(--accent)" }}>
                  ✓
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
