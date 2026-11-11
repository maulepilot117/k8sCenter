import {
  type KeyboardEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { ApiError, apiGet } from "@/lib/api.ts";
import { connectionStatusColor } from "@/lib/status-colors.ts";
import {
  clusterEpoch,
  LOCAL_CLUSTER_ID,
  LOCAL_GENERATION,
  selectedCluster,
  selectedClusterGeneration,
  switchCluster,
  UNKNOWN_GENERATION,
} from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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
 * A remote row with no createdAt (an older backend) falls back to the unknown
 * sentinel, which forces a cache miss rather than a wrong hit.
 */
function generationOf(c: ClusterListItem): string {
  if (c.isLocal || c.id === LOCAL_CLUSTER_ID) return LOCAL_GENERATION;
  return c.createdAt || UNKNOWN_GENERATION;
}

function toOption(c: ClusterListItem): ClusterOption {
  // Canonicalize the local row onto the id the backend resolves locally. A
  // deployment whose local cluster was registered under a different id would
  // otherwise become a second, separately-selectable "local".
  const id = c.isLocal ? LOCAL_CLUSTER_ID : c.id;
  return {
    id,
    label: c.displayName || c.name || id,
    generation: generationOf(c),
    status: c.status || "unknown",
  };
}

/**
 * An entry for a selection the registry did not return — a cluster deleted by
 * another admin, or a target restored from a build that stored only the id.
 * Rendering it keeps the trigger naming the cluster requests actually go to,
 * instead of silently claiming "local".
 */
function unavailableOption(id: string, generation: string): ClusterOption {
  return { id, label: id, generation, status: "unknown" };
}

/**
 * The options to show before (or instead of) an authoritative list: always
 * local, plus the current selection when it is something else.
 */
function fallbackOptions(): ClusterOption[] {
  const id = selectedCluster.peek();
  if (id === LOCAL_CLUSTER_ID) return [LOCAL_OPTION];
  return [
    LOCAL_OPTION,
    unavailableOption(id, selectedClusterGeneration.peek()),
  ];
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
  // Seeded from the persisted selection, not unconditionally from local:
  // `selectedCluster` hydrates to the restored id immediately, and a trigger
  // that says "local" while other islands already send a remote X-Cluster-ID
  // is a manufactured reading of state we actually know.
  const [options, setOptions] = useState<ClusterOption[]>(fallbackOptions);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const [loadFailed, setLoadFailed] = useState(false);
  const isClient = useIsClient();
  const selected = useSignalValue(selectedCluster);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  /**
   * Loads the cluster registry and reconciles the restored selection.
   *
   * Reconciliation is deliberately asymmetric, and each branch is keyed to
   * what the response actually proves:
   *
   * - **403 / 503** — definitive. `/v1/clusters` is admin-only and this call
   *   is pinned to the local cluster, so these say "this identity has no
   *   usable remote cluster" (not an admin, or no registry at all). Fail
   *   closed to local; otherwise a demoted admin keeps a remote target that
   *   403s every request in the app.
   * - **any other failure** — uninformative. A dropped connection or a 500 is
   *   an absence of information, not evidence the cluster is gone. Keep the
   *   selection; moving the operator on a failed request is exactly the
   *   silent retargeting this unit exists to prevent.
   * - **200, selection present** — refresh its generation (a re-registration
   *   under a recycled id must not keep the old one).
   * - **200, selection absent** — keep it and render it as unavailable. The
   *   operator can see something is wrong and choose. The common cause,
   *   deleting the cluster you are on, is handled at its source in
   *   ClusterManager rather than by silently retargeting here.
   */
  const loadClusters = useCallback(
    async (signal?: AbortSignal): Promise<void> => {
      try {
        const res = await apiGet<ClusterListItem[]>("/v1/clusters", {
          // The list itself is not cluster-scoped, but an unpinned request
          // would address whatever cluster is selected — including one that
          // has since been deleted. Ask the local cluster, always.
          clusterId: LOCAL_CLUSTER_ID,
          signal,
        });
        if (signal?.aborted) return;

        const rows = Array.isArray(res.data) ? res.data : [];
        const seen = new Set<string>();
        const mapped: ClusterOption[] = [];
        for (const row of rows) {
          const o = toOption(row);
          // A duplicate id would produce duplicate React keys and duplicate
          // DOM ids, which breaks aria-activedescendant targeting.
          if (!o.id || seen.has(o.id)) continue;
          seen.add(o.id);
          mapped.push(o);
        }
        if (!seen.has(LOCAL_CLUSTER_ID)) mapped.unshift(LOCAL_OPTION);

        const current = selectedCluster.peek();
        const match = mapped.find((o) => o.id === current);
        const selectionIsKnown = Boolean(match) || current === LOCAL_CLUSTER_ID;
        setOptions(
          selectionIsKnown
            ? mapped
            : [
                ...mapped,
                unavailableOption(current, selectedClusterGeneration.peek()),
              ],
        );
        if (match) switchCluster(match.id, match.generation);
        setLoadFailed(false);
      } catch (err) {
        if (signal?.aborted) return;
        const status = err instanceof ApiError ? err.status : 0;
        if (status === 403 || status === 503) {
          switchCluster(LOCAL_CLUSTER_ID, LOCAL_GENERATION);
          setOptions([LOCAL_OPTION]);
          setLoadFailed(false);
          return;
        }
        setOptions(fallbackOptions());
        setLoadFailed(true);
      }
    },
    [],
  );

  useEffect(() => {
    if (!IS_BROWSER) return;
    const controller = new AbortController();
    loadClusters(controller.signal);
    return () => controller.abort();
  }, [loadClusters]);

  // Move focus to the listbox once it has actually rendered, so arrow keys
  // drive aria-activedescendant. The click handler cannot do it: the listbox
  // is rendered only after the handler returns. preventScroll keeps the
  // browser from scrolling an ancestor to reveal the list.
  useEffect(() => {
    if (open) listRef.current?.focus({ preventScroll: true });
  }, [open]);

  // The restored selection is known only in the browser, so the server markup
  // names the local cluster and the client then shows the restored one.
  const current = isClient
    ? (options.find((o) => o.id === selected) ?? LOCAL_OPTION)
    : LOCAL_OPTION;

  /**
   * Commits a switch, then reloads.
   *
   * The reload is the whole invalidation strategy, and it is deliberate.
   * Nothing else in the app reacts to a cluster change: `ResourceTable`'s data
   * effect keys on `[kind, ns, enableWS]`, the resource WebSocket carries no
   * cluster dimension, and ~50 islands read nothing from this module. Without
   * a hard boundary a table keeps showing the previous cluster's rows while
   * `executeAction` sends those rows' paths to the *new* cluster — deleting
   * the same-named workload in the wrong place.
   *
   * Reloading remounts every island against the new target in one step and
   * cannot be forgotten by a future island. The reactive alternative — pin
   * each row's action with `currentTarget()` and add a cluster dep to every
   * data effect — is the better long-term shape and belongs to the unit that
   * can touch all of those files. The persisted selection is written
   * synchronously by the `switchCluster` batch before this runs, so the new
   * target survives the reload.
   */
  function choose(o: ClusterOption) {
    setOpen(false);
    const before = clusterEpoch.peek();
    switchCluster(o.id, o.generation);
    if (clusterEpoch.peek() === before) {
      // Re-selected the active cluster: nothing changed, so don't reload.
      buttonRef.current?.focus();
      return;
    }
    if (IS_BROWSER) globalThis.location.reload();
  }

  function openList() {
    const idx = options.findIndex((o) => o.id === selectedCluster.peek());
    setActiveIndex(idx >= 0 ? idx : 0);
    setOpen(true);
    // The list is fetched once on mount; a transient failure would otherwise
    // strand the operator with no way to retry short of a full page reload.
    if (loadFailed) loadClusters();
  }

  function onButtonKeyDown(e: KeyboardEvent<HTMLButtonElement>) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter") {
      e.preventDefault();
      openList();
    }
  }

  function onListKeyDown(e: KeyboardEvent<HTMLUListElement>) {
    const items = options;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        setActiveIndex((i) => (i + 1) % items.length);
        break;
      case "ArrowUp":
        e.preventDefault();
        setActiveIndex((i) => (i - 1 + items.length) % items.length);
        break;
      case "Home":
        e.preventDefault();
        setActiveIndex(0);
        break;
      case "End":
        e.preventDefault();
        setActiveIndex(items.length - 1);
        break;
      case "Enter":
      case " ":
        e.preventDefault();
        if (items[activeIndex]) choose(items[activeIndex]);
        break;
      case "Escape":
        e.preventDefault();
        setOpen(false);
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
      onBlur={(e) => {
        const next = e.relatedTarget as Node | null;
        if (next && rootRef.current?.contains(next)) return;
        setOpen(false);
      }}
    >
      <button
        ref={buttonRef}
        type="button"
        aria-label={`Active cluster: ${current.label}. Change cluster`}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls="cluster-switcher-list"
        // Keep focus inside the wrapper on pointer-down so the focusout
        // containment check passes and the click handler sees the real open
        // state. Without it, a browser that does not focus buttons on
        // mousedown closes the list and immediately reopens it.
        onMouseDown={(e) => {
          e.preventDefault();
          buttonRef.current?.focus();
        }}
        onClick={() => {
          if (open) {
            setOpen(false);
          } else {
            openList();
          }
        }}
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
            background: connectionStatusColor(current.status),
            flexShrink: 0,
          }}
        />
        <span style={{ fontWeight: 500 }}>{current.label}</span>
        <span aria-hidden="true" style={{ color: "var(--text-muted)" }}>
          ▾
        </span>
      </button>

      {open && (
        <ul
          ref={listRef}
          id="cluster-switcher-list"
          role="listbox"
          tabIndex={0}
          aria-label="Clusters"
          // Chrome surface — the glass utilities own the background, border
          // and elevation, so no literal colour is needed here.
          className="glass-elevated"
          aria-activedescendant={`cluster-option-${
            options[activeIndex]?.id ?? ""
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
            outline: "none",
            zIndex: 50,
          }}
        >
          {options.map((o, i) => (
            <li
              key={o.id}
              id={`cluster-option-${o.id}`}
              role="option"
              aria-selected={o.id === selected}
              // Pointer selection must beat the listbox's own blur handler,
              // which would otherwise close the list before the click lands.
              onMouseDown={(e) => {
                e.preventDefault();
                choose(o);
              }}
              onMouseEnter={() => {
                setActiveIndex(i);
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
                background:
                  i === activeIndex ? "var(--bg-hover)" : "transparent",
              }}
            >
              <span
                style={{
                  width: "8px",
                  height: "8px",
                  borderRadius: "50%",
                  background: connectionStatusColor(o.status),
                  flexShrink: 0,
                }}
              />
              <span style={{ flex: 1 }}>{o.label}</span>
              {o.id === selected && (
                <span aria-hidden="true" style={{ color: "var(--accent)" }}>
                  ✓
                </span>
              )}
            </li>
          ))}
          {loadFailed && (
            <li
              role="option"
              aria-selected={false}
              aria-disabled="true"
              style={{
                padding: "6px 8px",
                fontSize: "12px",
                color: "var(--text-muted)",
              }}
            >
              Cluster list unavailable — reopen to retry
            </li>
          )}
        </ul>
      )}
    </div>
  );
}
