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
    <tr class="hover:bg-hover/30">
      <td class="px-3 py-2">
        <div class="flex items-center gap-1 font-medium text-text-primary">
          <span>{workloadLabel}</span>
          {!w.workloadKindConfident && (
            <button
              type="button"
              class="text-text-muted cursor-help ml-0.5"
              aria-label="Workload kind inferred from ReplicaSet name (no owner-reference lookup)"
              title="Workload kind inferred from ReplicaSet name (no owner-reference lookup)"
            >
              *
            </button>
          )}
        </div>
      </td>
      <td class="px-3 py-2">
        <MTLSStateBadge state={w.state} />
      </td>
      <td class="px-3 py-2">
        <div class="flex flex-col gap-0.5">
          <MTLSSourceBadge source={w.source} />
          {defaultSubLine && (
            <span class="text-xs text-text-muted">{defaultSubLine}</span>
          )}
        </div>
      </td>
      <td class="px-3 py-2 text-sm text-text-secondary">{istioMode}</td>
      <td class="px-3 py-2 text-sm text-text-secondary">{sourceDetail}</td>
    </tr>
  );
}

/** The drill-down table for a namespace card's workloads. */
function WorkloadTable({ workloads }: { workloads: WorkloadMTLS[] }) {
  return (
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border-primary bg-surface">
            <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
              Workload
            </th>
            <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
              State
            </th>
            <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
              Source
            </th>
            <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
              Istio Mode
            </th>
            <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
              Source Detail
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border-subtle">
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
    <div class="rounded-lg border border-border-primary bg-bg-elevated overflow-hidden">
      <button
        type="button"
        class="w-full px-4 py-4 flex items-center justify-between text-left hover:bg-hover/20 transition-colors"
        onClick={onToggle}
        aria-expanded={expanded}
      >
        <div class="flex flex-col gap-1">
          <span class="font-semibold text-text-primary">{card.namespace}</span>
          <div class="flex items-center gap-2">
            <span
              class="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium"
              style={{
                color: pillColor,
                backgroundColor:
                  `color-mix(in srgb, ${pillColor} 15%, transparent)`,
              }}
            >
              {pillLabel}
            </span>
            {subLine && <span class="text-xs text-text-muted">{subLine}</span>}
          </div>
        </div>
        <span
          class="text-text-muted text-sm transition-transform"
          style={{ transform: expanded ? "rotate(180deg)" : "rotate(0deg)" }}
        >
          ▾
        </span>
      </button>
      {expanded && (
        <div class="border-t border-border-primary">
          <WorkloadTable workloads={card.all} />
        </div>
      )}
    </div>
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
    <div class="rounded-lg border border-border-primary bg-bg-elevated overflow-hidden">
      <button
        type="button"
        class="w-full px-4 py-4 flex items-center justify-between text-left hover:bg-hover/20 transition-colors"
        onClick={onToggle}
        aria-expanded={expanded}
      >
        <div class="flex flex-col gap-1">
          <span class="font-semibold text-text-muted">Not in mesh</span>
          <span class="text-xs text-text-muted">
            {totalWorkloads} workload{totalWorkloads !== 1 ? "s" : ""} across
            {" "}
            {cards.length} namespace{cards.length !== 1 ? "s" : ""}
          </span>
        </div>
        <span
          class="text-text-muted text-sm transition-transform"
          style={{ transform: expanded ? "rotate(180deg)" : "rotate(0deg)" }}
        >
          ▾
        </span>
      </button>
      {expanded && (
        <div class="border-t border-border-primary">
          <WorkloadTable workloads={allWorkloads} />
        </div>
      )}
    </div>
  );
}

/** Error banner row for a single errors-map key. */
function ErrorBanner(
  { errorKey, message }: { errorKey: string; message: string },
) {
  const isWarn = WARN_KEYS.has(errorKey);
  const color = isWarn ? "var(--warning)" : "var(--danger)";
  return (
    <div
      class="rounded-lg px-4 py-3 text-sm flex items-start gap-3"
      style={{
        backgroundColor: `color-mix(in srgb, ${color} 12%, transparent)`,
        border: `1px solid color-mix(in srgb, ${color} 30%, transparent)`,
      }}
    >
      <span class="font-medium shrink-0" style={{ color }}>
        {isWarn ? "Notice" : "Error"}
      </span>
      <span class="text-text-primary">{message}</span>
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
    <div class="p-6">
      <div class="flex items-center justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">mTLS Posture</h1>
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
      <p class="text-sm text-text-muted mb-6">
        Per-namespace mutual TLS posture across your service mesh.
      </p>

      {/* Namespace filter */}
      <div class="mb-6 flex items-center gap-3">
        <label
          class="text-sm font-medium text-text-secondary shrink-0"
          htmlFor="mtls-ns-filter"
        >
          Namespace
        </label>
        <input
          id="mtls-ns-filter"
          type="text"
          class="rounded border border-border-primary px-3 py-1.5 text-sm bg-bg-base text-text-primary max-w-xs"
          placeholder="All namespaces"
          value={namespace.value}
          onInput={(e) =>
            handleNamespaceChange((e.target as HTMLInputElement).value)}
        />
        {namespace.value && (
          <button
            type="button"
            class="text-xs text-text-muted hover:text-text-primary"
            onClick={() => handleNamespaceChange("")}
          >
            Clear
          </button>
        )}
      </div>

      {/* Error / notice banners */}
      {errorKeys.length > 0 && (
        <div class="flex flex-col gap-2 mb-6">
          {errorKeys.map((key) => (
            <ErrorBanner key={key} errorKey={key} message={errors[key]} />
          ))}
        </div>
      )}

      {/* Loading state */}
      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {/* Error state */}
      {!loading.value && error.value && (
        <p class="text-sm text-danger py-4">{error.value}</p>
      )}

      {/* Empty state */}
      {!loading.value && !error.value && workloads.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">No workloads in mesh.</p>
        </div>
      )}

      {/* Main card grid */}
      {!loading.value && !error.value && meshedCards.length > 0 && (
        <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-6">
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
        <div class="mt-4">
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
