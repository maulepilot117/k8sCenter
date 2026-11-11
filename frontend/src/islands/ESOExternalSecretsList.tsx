import { useCallback, useEffect, useRef, useState } from "react";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type { ExternalSecret } from "@/lib/eso-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import ESOBulkRefreshDialog from "@/src/islands/ESOBulkRefreshDialog.tsx";
import ExternalSecretWizard from "@/src/islands/ExternalSecretWizard.tsx";
import { filterByNamespace, selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

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
  const isClient = useIsClient();
  const globalNamespace = useSignalValue(selectedNamespace);
  const [items, setItems] = useState<ExternalSecret[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [namespace, setNamespace, namespaceRef] = useStateRef("");
  const [search, setSearch] = useState("");
  const [detected, setDetected] = useState<boolean | null>(null);
  const [showRefreshDialog, setShowRefreshDialog] = useState(false);
  const [wizardOpen, setWizardOpen] = useState(false);

  const fetchSeq = useRef(0);
  const debounceHandle = useRef<ReturnType<typeof setTimeout> | null>(null);

  const fetchData = useCallback(async () => {
    const seq = ++fetchSeq.current;
    try {
      const ns = namespaceRef.current.trim() || undefined;
      const res = await esoApi.listExternalSecrets(ns);
      if (seq !== fetchSeq.current) return;
      setItems(Array.isArray(res.data) ? res.data : []);
      setError(null);
    } catch {
      if (seq !== fetchSeq.current) return;
      setError("Failed to load ExternalSecrets");
    }
  }, []);

  useEffect(() => {
    (async () => {
      try {
        const statusRes = await esoApi.status();
        const present = statusRes.data?.detected !== false;
        setDetected(present);
        if (present) await fetchData();
      } catch {
        setDetected(true);
        await fetchData();
      } finally {
        setLoading(false);
      }
    })();
    return () => {
      if (debounceHandle.current !== null) {
        clearTimeout(debounceHandle.current);
        debounceHandle.current = null;
      }
    };
  }, [fetchData]);

  useEffect(() => {
    const params = new URLSearchParams(globalThis.location.search);
    if (params.get("action") === "create") setWizardOpen(true);
  }, []);

  function handleNamespaceChange(value: string) {
    setNamespace(value);
    if (debounceHandle.current !== null) clearTimeout(debounceHandle.current);
    debounceHandle.current = setTimeout(() => {
      debounceHandle.current = null;
      fetchData();
    }, NAMESPACE_DEBOUNCE_MS);
  }

  if (!isClient) return null;

  if (!loading && detected === false) {
    return (
      <div className="p-6">
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
          }}
          className="mb-6"
        >
          ExternalSecrets
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const byNamespace = filterByNamespace(items, globalNamespace);
  const filtered = byNamespace.filter((es) => {
    if (!search) return true;
    const q = search.toLowerCase();
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
    <div className="p-6">
      {/* Page header */}
      <div className="flex items-start justify-between mb-1">
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
        <div className="flex items-center gap-2">
          {namespace.trim() !== "" && (
            <button
              type="button"
              onClick={() => setShowRefreshDialog(true)}
              className={inputStyle}
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
            onClick={() => setWizardOpen(true)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-opacity hover:opacity-90"
            style={{
              background: "var(--accent)",
              color: "var(--bg-base)",
            }}
          >
            + New ExternalSecret
          </button>
        </div>
      </div>

      {showRefreshDialog && (
        <ESOBulkRefreshDialog
          action="refresh_namespace"
          target={{ namespace: namespace.trim() }}
          onClose={() => setShowRefreshDialog(false)}
        />
      )}
      <p
        className="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        ExternalSecrets sync data from a SecretStore into a Kubernetes Secret.
      </p>

      {/* Filters */}
      <div className="mb-4 flex flex-wrap items-center gap-4">
        <div className="flex items-center gap-2">
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
            className={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="All namespaces"
            value={namespace}
            aria-describedby="eso-es-ns-hint"
            onChange={(e) => handleNamespaceChange(e.currentTarget.value)}
          />
          <span id="eso-es-ns-hint" className="sr-only">
            Filter ExternalSecrets by namespace; updates after a brief pause.
          </span>
        </div>
        <div className="flex items-center gap-2">
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
            className={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="name, store, target…"
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {byNamespace.length} ExternalSecrets
          {byNamespace.length < items.length && ` (${items.length} total)`}
        </span>
      </div>

      {loading && (
        <div className="flex justify-center py-12">
          <Spinner className="text-brand" />
        </div>
      )}

      {!loading && error && (
        <p style={{ fontSize: "13px", color: "var(--error)" }} className="py-4">
          {error}
        </p>
      )}

      {!loading && !error && filtered.length > 0 && (
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
                <span className="inline-flex items-center gap-2">
                  <StatusDot status={esoToDot(es.status)} />
                  <a
                    href={`/external-secrets/external-secrets/${encodeURIComponent(
                      es.namespace,
                    )}/${encodeURIComponent(es.name)}`}
                    className="hover:underline"
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
                  className="tabular-nums"
                >
                  {es.lastSyncTime ? timeAgo(es.lastSyncTime) : "—"}
                </span>
              ),
            },
            onClick: () => {
              globalThis.location.href = `/external-secrets/external-secrets/${encodeURIComponent(
                es.namespace,
              )}/${encodeURIComponent(es.name)}`;
            },
          }))}
        />
      )}

      {!loading &&
        !error &&
        filtered.length === 0 &&
        byNamespace.length > 0 && (
          <div
            className="text-center py-12 rounded-lg"
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

      {!loading && !error && byNamespace.length === 0 && (
        <div
          className="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p
            className="mb-3"
            style={{ fontSize: "13px", color: "var(--text-muted)" }}
          >
            No ExternalSecrets in this namespace. Create one to start syncing
            secrets from a SecretStore.
          </p>
          <button
            type="button"
            onClick={() => setWizardOpen(true)}
            className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg text-sm font-medium transition-opacity hover:opacity-90"
            style={{ background: "var(--accent)", color: "var(--bg-base)" }}
          >
            New ExternalSecret
          </button>
        </div>
      )}

      {wizardOpen && (
        <ExternalSecretWizard onClose={() => setWizardOpen(false)} />
      )}
    </div>
  );
}
