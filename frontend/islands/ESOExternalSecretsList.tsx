import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { esoApi } from "@/lib/eso-api.ts";
import ExternalSecretWizard from "@/islands/ExternalSecretWizard.tsx";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import ESOBulkRefreshDialog from "@/islands/ESOBulkRefreshDialog.tsx";
import { timeAgo } from "@/lib/timeAgo.ts";
import { filterByNamespace, selectedNamespace } from "@/lib/namespace.ts";
import type { ExternalSecret } from "@/lib/eso-types.ts";
import ResourceTable from "@/components/ui/ResourceTable.tsx";

const NAMESPACE_DEBOUNCE_MS = 300;

/** Map ESO status → StatusDot tone. */
function esoToDot(
  status: string,
): "success" | "error" | "warning" | "info" | "neutral" {
  switch (status) {
    case "Synced":
      return "success";
    case "SyncFailed":
      return "error";
    case "Stale":
      return "warning";
    case "Drifted":
      return "info";
    default:
      return "neutral";
  }
}

export default function ESOExternalSecretsList() {
  const items = useSignal<ExternalSecret[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const namespace = useSignal("");
  const search = useSignal("");
  const detected = useSignal<boolean | null>(null);
  const showRefreshDialog = useSignal(false);
  const wizardOpen = useSignal(false);

  const fetchSeq = useRef(0);
  const debounceHandle = useRef<number | null>(null);

  async function fetchData() {
    const seq = ++fetchSeq.current;
    try {
      const ns = namespace.value.trim() || undefined;
      const res = await esoApi.listExternalSecrets(ns);
      if (seq !== fetchSeq.current) return;
      items.value = Array.isArray(res.data) ? res.data : [];
      error.value = null;
    } catch {
      if (seq !== fetchSeq.current) return;
      error.value = "Failed to load ExternalSecrets";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    (async () => {
      try {
        const statusRes = await esoApi.status();
        const present = statusRes.data?.detected !== false;
        detected.value = present;
        if (present) await fetchData();
      } catch {
        detected.value = true;
        await fetchData();
      } finally {
        loading.value = false;
      }
    })();
    return () => {
      if (debounceHandle.current !== null) {
        clearTimeout(debounceHandle.current);
        debounceHandle.current = null;
      }
    };
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    if (params.get("action") === "create") wizardOpen.value = true;
  }, []);

  function handleNamespaceChange(value: string) {
    namespace.value = value;
    if (debounceHandle.current !== null) clearTimeout(debounceHandle.current);
    debounceHandle.current = setTimeout(() => {
      debounceHandle.current = null;
      fetchData();
    }, NAMESPACE_DEBOUNCE_MS);
  }

  if (!IS_BROWSER) return null;

  if (!loading.value && detected.value === false) {
    return (
      <div class="p-6">
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
          }}
          class="mb-6"
        >
          ExternalSecrets
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const nsByGlobal = selectedNamespace.value;
  const byNamespace = filterByNamespace(items.value, nsByGlobal);
  const filtered = byNamespace.filter((es) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      es.name.toLowerCase().includes(q) ||
      es.namespace.toLowerCase().includes(q) ||
      es.storeRef.name.toLowerCase().includes(q) ||
      (es.targetSecretName ?? "").toLowerCase().includes(q)
    );
  });

  const inputStyle =
    "rounded-lg px-3 py-1.5 text-sm max-w-xs focus:outline-none focus:ring-1";

  return (
    <div class="p-6">
      {/* Page header */}
      <div class="flex items-start justify-between mb-1">
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
          }}
        >
          ExternalSecrets
        </h1>
        <div class="flex items-center gap-2">
          {namespace.value.trim() !== "" && (
            <button
              type="button"
              onClick={() => (showRefreshDialog.value = true)}
              class={inputStyle}
              style={{
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-subtle)",
                color: "var(--text-primary)",
              }}
            >
              Refresh namespace
            </button>
          )}
          <button
            type="button"
            onClick={() => (wizardOpen.value = true)}
            class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-opacity hover:opacity-90"
            style={{
              background: "var(--accent)",
              color: "var(--bg-base)",
            }}
          >
            + New ExternalSecret
          </button>
        </div>
      </div>

      {showRefreshDialog.value && (
        <ESOBulkRefreshDialog
          action="refresh_namespace"
          target={{ namespace: namespace.value.trim() }}
          onClose={() => (showRefreshDialog.value = false)}
        />
      )}
      <p
        class="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        ExternalSecrets sync data from a SecretStore into a Kubernetes Secret.
      </p>

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex items-center gap-2">
          <label
            style={{
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
            }}
            htmlFor="eso-es-ns"
          >
            Namespace
          </label>
          <input
            id="eso-es-ns"
            type="text"
            class={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="All namespaces"
            value={namespace.value}
            aria-describedby="eso-es-ns-hint"
            onInput={(e) =>
              handleNamespaceChange((e.target as HTMLInputElement).value)}
          />
          <span id="eso-es-ns-hint" class="sr-only">
            Filter ExternalSecrets by namespace; updates after a brief pause.
          </span>
        </div>
        <div class="flex items-center gap-2">
          <label
            style={{
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
            }}
            htmlFor="eso-es-search"
          >
            Search
          </label>
          <input
            id="eso-es-search"
            type="text"
            class={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="name, store, target…"
            value={search.value}
            onInput={(e) => {
              search.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {byNamespace.length} ExternalSecrets
          {byNamespace.length < items.value.length &&
            ` (${items.value.length} total)`}
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {!loading.value && error.value && (
        <p style={{ fontSize: "13px", color: "var(--error)" }} class="py-4">
          {error.value}
        </p>
      )}

      {!loading.value && !error.value && filtered.length > 0 && (
        <ResourceTable
          columns={[
            { key: "name", label: "Name", width: "1.6fr" },
            { key: "namespace", label: "Namespace", width: "120px" },
            { key: "status", label: "Status", width: "120px" },
            { key: "store", label: "Store", width: "1fr" },
            { key: "targetSecret", label: "Target Secret", width: "1fr" },
            { key: "lastSync", label: "Last Sync", width: "90px" },
          ]}
          rows={filtered.map((es) => ({
            id: es.uid,
            cells: {
              name: (
                <span class="inline-flex items-center gap-2">
                  <StatusDot status={esoToDot(es.status)} />
                  <a
                    href={`/external-secrets/external-secrets/${
                      encodeURIComponent(es.namespace)
                    }/${encodeURIComponent(es.name)}`}
                    class="hover:underline"
                    style={{
                      fontSize: "13px",
                      fontWeight: 500,
                      fontFamily: "var(--font-mono, monospace)",
                      color: "var(--text-primary)",
                    }}
                    onClick={(e) => e.stopPropagation()}
                  >
                    {es.name}
                  </a>
                </span>
              ),
              namespace: (
                <span style={{ fontSize: "13px", color: "var(--text-muted)" }}>
                  {es.namespace}
                </span>
              ),
              status: <StatusBadge status={es.status} />,
              store: (
                <span style={{ fontSize: "13px", color: "var(--text-muted)" }}>
                  {es.storeRef.name}
                </span>
              ),
              targetSecret: (
                <span style={{ fontSize: "13px", color: "var(--text-muted)" }}>
                  {es.targetSecretName ?? "—"}
                </span>
              ),
              lastSync: (
                <span
                  style={{ fontSize: "12px", color: "var(--text-muted)" }}
                  class="tabular-nums"
                >
                  {es.lastSyncTime ? timeAgo(es.lastSyncTime) : "—"}
                </span>
              ),
            },
            onClick: () => {
              globalThis.location.href = `/external-secrets/external-secrets/${
                encodeURIComponent(es.namespace)
              }/${encodeURIComponent(es.name)}`;
            },
          }))}
        />
      )}

      {!loading.value && !error.value && filtered.length === 0 &&
        byNamespace.length > 0 && (
        <div
          class="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            No ExternalSecrets match your filters.
          </p>
        </div>
      )}

      {!loading.value && !error.value && byNamespace.length === 0 && (
        <div
          class="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p
            class="mb-3"
            style={{ fontSize: "13px", color: "var(--text-muted)" }}
          >
            No ExternalSecrets in this namespace. Create one to start syncing
            secrets from a SecretStore.
          </p>
          <button
            type="button"
            onClick={() => (wizardOpen.value = true)}
            class="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg text-sm font-medium transition-opacity hover:opacity-90"
            style={{ background: "var(--accent)", color: "var(--bg-base)" }}
          >
            New ExternalSecret
          </button>
        </div>
      )}

      {wizardOpen.value && (
        <ExternalSecretWizard onClose={() => (wizardOpen.value = false)} />
      )}
    </div>
  );
}
