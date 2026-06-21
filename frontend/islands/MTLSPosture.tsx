import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { meshApi } from "@/lib/mesh-api.ts";
import {
  MTLSSourceBadge,
  MTLSStateBadge,
} from "@/components/ui/MeshBadges.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import type { MTLSPostureResponse, WorkloadMTLS } from "@/lib/mesh-types.ts";

/** Warning-level error keys (metric/truncation issues) vs. error-level (pod/policy fetch failures). */
const WARN_KEYS = new Set(["prometheus-cross-check", "truncated"]);

/** Debounce window (ms) for namespace-input → re-fetch. Long enough to
 *  coalesce typing, short enough to feel responsive after the user stops. */
const NAMESPACE_DEBOUNCE_MS = 300;

/** Per-namespace aggregated card data. */
interface NamespaceCard {
  namespace: string;
  /** Workloads that are in-mesh (not unmeshed). */
  meshed: WorkloadMTLS[];
  /** All workloads including unmeshed. */
  all: WorkloadMTLS[];
  active: number;
  inactive: number;
  mixed: number;
  unmeshed: number;
}

function classifyCard(
  card: NamespaceCard,
): "green" | "yellow" | "unmeshed-only" {
  const { active, inactive, mixed, unmeshed } = card;
  const totalMeshed = active + inactive + mixed;

  if (totalMeshed === 0) {
    // All workloads are unmeshed
    return "unmeshed-only";
  }
  if (inactive === 0 && mixed === 0 && unmeshed === 0) {
    // Every workload is active
    return "green";
  }
  // Any inactive/mixed, or some unmeshed alongside meshed
  return "yellow";
}

function buildCards(workloads: WorkloadMTLS[]): NamespaceCard[] {
  const map = new Map<string, NamespaceCard>();

  for (const w of workloads) {
    if (!map.has(w.namespace)) {
      map.set(w.namespace, {
        namespace: w.namespace,
        meshed: [],
        all: [],
        active: 0,
        inactive: 0,
        mixed: 0,
        unmeshed: 0,
      });
    }
    const card = map.get(w.namespace)!;
    card.all.push(w);
    if (w.state === "active") card.active++;
    else if (w.state === "inactive") card.inactive++;
    else if (w.state === "mixed") card.mixed++;
    else if (w.state === "unmeshed") card.unmeshed++;
    if (w.state !== "unmeshed") {
      card.meshed.push(w);
    }
  }

  return Array.from(map.values()).sort((a, b) =>
    a.namespace.localeCompare(b.namespace)
  );
}

/** A single workload row in the drill-down table. */
function WorkloadRow({ w }: { w: WorkloadMTLS }) {
  const hovered = useSignal(false);
  const kindLabel = w.workloadKind ?? "—";
  const workloadLabel = `${kindLabel}/${w.workload}`;

  const isIstio = w.mesh === "istio";
  const istioMode = isIstio ? (w.istioMode || "—") : "—";
  const sourceDetail = isIstio ? (w.sourceDetail || "—") : "—";

  let defaultSubLine: string | null = null;
  if (w.source === "default") {
    defaultSubLine = w.mesh === "linkerd"
      ? "Default-on (Linkerd)"
      : "No explicit policy";
  }

  return (
    <tr
      style={{
        borderBottom: "1px solid var(--border-primary)",
        background: hovered.value
          ? "color-mix(in srgb, var(--accent) 5%, transparent)"
          : "transparent",
      }}
      onMouseEnter={() => {
        hovered.value = true;
      }}
      onMouseLeave={() => {
        hovered.value = false;
      }}
    >
      <td style={{ padding: "8px 12px" }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "4px",
            fontWeight: 500,
            color: "var(--text-primary)",
          }}
        >
          <span>{workloadLabel}</span>
          {!w.workloadKindConfident && (
            <button
              type="button"
              style={{
                color: "var(--text-muted)",
                cursor: "help",
                marginLeft: "2px",
                background: "none",
                border: "none",
                padding: 0,
                font: "inherit",
              }}
              aria-label="Workload kind inferred from ReplicaSet name (no owner-reference lookup)"
              title="Workload kind inferred from ReplicaSet name (no owner-reference lookup)"
            >
              *
            </button>
          )}
        </div>
      </td>
      <td style={{ padding: "8px 12px" }}>
        <MTLSStateBadge state={w.state} />
      </td>
      <td style={{ padding: "8px 12px" }}>
        <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
          <MTLSSourceBadge source={w.source} />
          {defaultSubLine && (
            <span style={{ fontSize: "11px", color: "var(--text-muted)" }}>
              {defaultSubLine}
            </span>
          )}
        </div>
      </td>
      <td
        style={{
          padding: "8px 12px",
          fontSize: "13px",
          color: "var(--text-secondary)",
        }}
      >
        {istioMode}
      </td>
      <td
        style={{
          padding: "8px 12px",
          fontSize: "13px",
          color: "var(--text-secondary)",
        }}
      >
        {sourceDetail}
      </td>
    </tr>
  );
}

/** The drill-down table for a namespace card's workloads. */
function WorkloadTable({ workloads }: { workloads: WorkloadMTLS[] }) {
  return (
    <div style={{ overflowX: "auto" }}>
      <table
        style={{ width: "100%", fontSize: "13px", borderCollapse: "collapse" }}
      >
        <thead>
          <tr
            style={{
              borderBottom: "1px solid var(--border-primary)",
              background: "var(--bg-surface)",
            }}
          >
            <th
              style={{
                padding: "8px 12px",
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.07em",
                color: "var(--text-muted)",
                textAlign: "left",
              }}
            >
              Workload
            </th>
            <th
              style={{
                padding: "8px 12px",
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.07em",
                color: "var(--text-muted)",
                textAlign: "left",
              }}
            >
              State
            </th>
            <th
              style={{
                padding: "8px 12px",
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.07em",
                color: "var(--text-muted)",
                textAlign: "left",
              }}
            >
              Source
            </th>
            <th
              style={{
                padding: "8px 12px",
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.07em",
                color: "var(--text-muted)",
                textAlign: "left",
              }}
            >
              Istio Mode
            </th>
            <th
              style={{
                padding: "8px 12px",
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.07em",
                color: "var(--text-muted)",
                textAlign: "left",
              }}
            >
              Source Detail
            </th>
          </tr>
        </thead>
        <tbody>
          {workloads.map((w) => (
            <WorkloadRow key={`${w.namespace}/${w.workload}`} w={w} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** A single namespace posture card. */
function NamespacePostureCard(
  { card, expanded, onToggle }: {
    card: NamespaceCard;
    expanded: boolean;
    onToggle: () => void;
  },
) {
  const classification = classifyCard(card);
  const totalMeshed = card.active + card.inactive + card.mixed;
  const total = card.all.length;

  let pillColor: string;
  let pillLabel: string;
  let subLine: string | null = null;

  if (classification === "green") {
    pillColor = "var(--success)";
    pillLabel = `${card.active} of ${card.active} active`;
  } else if (classification === "yellow") {
    pillColor = "var(--warning)";
    pillLabel = `${card.active} of ${totalMeshed} active`;
    if (card.unmeshed > 0) {
      subLine = `(${card.unmeshed} of ${total} not in mesh)`;
    }
  } else {
    // unmeshed-only — shown separately, but defined here for completeness
    pillColor = "var(--text-muted)";
    pillLabel = `${total} workloads`;
  }

  return (
    <WidgetShell style={{ overflow: "hidden", padding: 0 }}>
      <button
        type="button"
        style={{
          width: "100%",
          padding: "16px",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          background: "transparent",
          border: "none",
          cursor: "pointer",
          textAlign: "left",
        }}
        onClick={onToggle}
        aria-expanded={expanded}
      >
        <div style={{ display: "flex", flexDirection: "column", gap: "4px" }}>
          <span style={{ fontWeight: 600, color: "var(--text-primary)" }}>
            {card.namespace}
          </span>
          <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                padding: "2px 8px",
                borderRadius: "9999px",
                fontSize: "11px",
                fontWeight: 500,
                fontVariantNumeric: "tabular-nums",
                color: pillColor,
                backgroundColor:
                  `color-mix(in srgb, ${pillColor} 15%, transparent)`,
              }}
            >
              {pillLabel}
            </span>
            {subLine && (
              <span style={{ fontSize: "11px", color: "var(--text-muted)" }}>
                {subLine}
              </span>
            )}
          </div>
        </div>
        <span
          style={{
            color: "var(--text-muted)",
            fontSize: "13px",
            transform: expanded ? "rotate(180deg)" : "rotate(0deg)",
            transition: "transform 0.15s ease",
          }}
        >
          ▾
        </span>
      </button>
      {expanded && (
        <div style={{ borderTop: "1px solid var(--border-primary)" }}>
          <WorkloadTable workloads={card.all} />
        </div>
      )}
    </WidgetShell>
  );
}

/** The "Not in mesh" collapsed section at the bottom. */
function NotInMeshSection(
  { cards, expanded, onToggle }: {
    cards: NamespaceCard[];
    expanded: boolean;
    onToggle: () => void;
  },
) {
  const totalWorkloads = cards.reduce((s, c) => s + c.all.length, 0);
  const allWorkloads = cards.flatMap((c) => c.all);

  return (
    <WidgetShell style={{ overflow: "hidden", padding: 0 }}>
      <button
        type="button"
        style={{
          width: "100%",
          padding: "16px",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          background: "transparent",
          border: "none",
          cursor: "pointer",
          textAlign: "left",
        }}
        onClick={onToggle}
        aria-expanded={expanded}
      >
        <div style={{ display: "flex", flexDirection: "column", gap: "4px" }}>
          <span style={{ fontWeight: 600, color: "var(--text-muted)" }}>
            Not in mesh
          </span>
          <span
            style={{
              fontSize: "11px",
              color: "var(--text-muted)",
              fontVariantNumeric: "tabular-nums",
            }}
          >
            {totalWorkloads} workload{totalWorkloads !== 1 ? "s" : ""} across
            {" "}
            {cards.length} namespace{cards.length !== 1 ? "s" : ""}
          </span>
        </div>
        <span
          style={{
            color: "var(--text-muted)",
            fontSize: "13px",
            transform: expanded ? "rotate(180deg)" : "rotate(0deg)",
            transition: "transform 0.15s ease",
          }}
        >
          ▾
        </span>
      </button>
      {expanded && (
        <div style={{ borderTop: "1px solid var(--border-primary)" }}>
          <WorkloadTable workloads={allWorkloads} />
        </div>
      )}
    </WidgetShell>
  );
}

/** Error banner row for a single errors-map key. */
function ErrorBanner(
  { errorKey, message }: { errorKey: string; message: string },
) {
  const isWarn = WARN_KEYS.has(errorKey);
  const color = isWarn ? "var(--warning)" : "var(--error)";
  return (
    <div
      style={{
        borderRadius: "9px",
        padding: "12px 16px",
        fontSize: "13px",
        display: "flex",
        alignItems: "flex-start",
        gap: "12px",
        backgroundColor: `color-mix(in srgb, ${color} 12%, transparent)`,
        border: `1px solid color-mix(in srgb, ${color} 30%, transparent)`,
      }}
    >
      <span style={{ color, fontWeight: 600, flexShrink: 0 }}>
        {isWarn ? "Notice" : "Error"}
      </span>
      <span style={{ color: "var(--text-primary)" }}>{message}</span>
    </div>
  );
}

export default function MTLSPosture() {
  const data = useSignal<MTLSPostureResponse | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const namespace = useSignal<string>("");
  const expandedNamespaces = useSignal<Record<string, boolean>>({});
  const expandedNotInMesh = useSignal(false);
  const refreshing = useSignal(false);

  // Sequence counter: every fetch increments and captures a token; a
  // response is applied only if its token is the latest. Stops slow
  // earlier responses from overwriting state from later, faster ones.
  const fetchSeq = useRef(0);
  // Debounce handle for namespace-input → fetch.
  const debounceHandle = useRef<number | null>(null);

  async function fetchData() {
    const seq = ++fetchSeq.current;
    try {
      const ns = namespace.value.trim() || undefined;
      const res = await meshApi.mtls({ namespace: ns });
      if (seq !== fetchSeq.current) return; // stale response — drop
      data.value = res.data ?? null;
      error.value = null;
    } catch {
      if (seq !== fetchSeq.current) return; // stale error — drop
      error.value = "Failed to load mTLS posture data";
    }
  }

  // Read namespace from URL on mount, then fetch
  useEffect(() => {
    if (!IS_BROWSER) return;
    const url = new URL(globalThis.location.href);
    const ns = url.searchParams.get("namespace") ?? "";
    namespace.value = ns;
    fetchData().then(() => {
      loading.value = false;
    });
    return () => {
      if (debounceHandle.current !== null) {
        clearTimeout(debounceHandle.current);
        debounceHandle.current = null;
      }
    };
  }, []);

  function handleNamespaceChange(newNs: string) {
    namespace.value = newNs;
    // Update URL so the view is deep-linkable
    const url = new URL(globalThis.location.href);
    if (newNs) {
      url.searchParams.set("namespace", newNs);
    } else {
      url.searchParams.delete("namespace");
    }
    globalThis.history.replaceState({}, "", url.toString());
    // Debounce the re-fetch so per-keystroke typing doesn't fan out N
    // concurrent requests. The sequence guard in fetchData also drops
    // any stragglers that survive the debounce.
    if (debounceHandle.current !== null) {
      clearTimeout(debounceHandle.current);
    }
    debounceHandle.current = setTimeout(() => {
      debounceHandle.current = null;
      fetchData();
    }, NAMESPACE_DEBOUNCE_MS);
  }

  async function handleRefresh() {
    // Cancel any pending debounced fetch — refresh wins.
    if (debounceHandle.current !== null) {
      clearTimeout(debounceHandle.current);
      debounceHandle.current = null;
    }
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  function toggleNamespace(ns: string) {
    expandedNamespaces.value = {
      ...expandedNamespaces.value,
      [ns]: !expandedNamespaces.value[ns],
    };
  }

  if (!IS_BROWSER) return null;

  const workloads = data.value?.workloads ?? [];
  const errors = data.value?.errors ?? {};
  const errorKeys = Object.keys(errors);

  const allCards = buildCards(workloads);
  const meshedCards = allCards.filter((c) =>
    classifyCard(c) !== "unmeshed-only"
  );
  const unmeshedOnlyCards = allCards.filter((c) =>
    classifyCard(c) === "unmeshed-only"
  );

  return (
    <div style={{ padding: "24px" }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: "4px",
        }}
      >
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          mTLS Posture
        </h1>
        {!loading.value && (
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing.value}
          >
            {refreshing.value ? "Refreshing..." : "Refresh"}
          </Button>
        )}
      </div>
      <p
        style={{
          fontSize: "13px",
          color: "var(--text-muted)",
          marginTop: "4px",
          marginBottom: "24px",
        }}
      >
        Per-namespace mutual TLS posture across your service mesh.
      </p>

      {/* Namespace filter */}
      <div
        style={{
          marginBottom: "24px",
          display: "flex",
          alignItems: "center",
          gap: "12px",
        }}
      >
        <label
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            flexShrink: 0,
          }}
          htmlFor="mtls-ns-filter"
        >
          Namespace
        </label>
        <input
          id="mtls-ns-filter"
          type="text"
          style={{
            borderRadius: "9px",
            border: "1px solid var(--border-primary)",
            background: "var(--bg-surface)",
            padding: "6px 12px",
            fontSize: "13px",
            color: "var(--text-primary)",
            fontFamily: "inherit",
            maxWidth: "320px",
          }}
          placeholder="All namespaces"
          value={namespace.value}
          onInput={(e) =>
            handleNamespaceChange((e.target as HTMLInputElement).value)}
        />
        {namespace.value && (
          <button
            type="button"
            style={{
              fontSize: "11px",
              color: "var(--text-muted)",
              background: "none",
              border: "none",
              cursor: "pointer",
              padding: 0,
              font: "inherit",
            }}
            onClick={() => handleNamespaceChange("")}
          >
            Clear
          </button>
        )}
      </div>

      {/* Error / notice banners */}
      {errorKeys.length > 0 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "8px",
            marginBottom: "24px",
          }}
        >
          {errorKeys.map((key) => (
            <ErrorBanner key={key} errorKey={key} message={errors[key]} />
          ))}
        </div>
      )}

      {/* Loading state */}
      {loading.value && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "48px 0",
          }}
        >
          <Spinner />
        </div>
      )}

      {/* Error state */}
      {!loading.value && error.value && (
        <p
          style={{ fontSize: "13px", color: "var(--error)", padding: "16px 0" }}
        >
          {error.value}
        </p>
      )}

      {/* Empty state */}
      {!loading.value && !error.value && workloads.length === 0 && (
        <WidgetShell>
          <div style={{ textAlign: "center", padding: "48px 24px" }}>
            <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>
              No workloads in mesh.
            </p>
          </div>
        </WidgetShell>
      )}

      {/* Main card grid */}
      {!loading.value && !error.value && meshedCards.length > 0 && (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(320px, 1fr))",
            gap: "16px",
            marginBottom: "24px",
          }}
        >
          {meshedCards.map((card) => (
            <NamespacePostureCard
              key={card.namespace}
              card={card}
              expanded={!!expandedNamespaces.value[card.namespace]}
              onToggle={() => toggleNamespace(card.namespace)}
            />
          ))}
        </div>
      )}

      {/* Not in mesh section */}
      {!loading.value && !error.value && unmeshedOnlyCards.length > 0 && (
        <div style={{ marginTop: "16px" }}>
          <NotInMeshSection
            cards={unmeshedOnlyCards}
            expanded={expandedNotInMesh.value}
            onToggle={() => {
              expandedNotInMesh.value = !expandedNotInMesh.value;
            }}
          />
        </div>
      )}
    </div>
  );
}
