/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type { PushSecret } from "@/lib/eso-types.ts";
import { filterByNamespace, selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

const NAMESPACE_DEBOUNCE_MS = 300;

/** Map PushSecret status → StatusDot tone. */
function pushToDot(
  status: string,
): "success" | "error" | "warning" | "info" | "neutral" {
  switch (status) {
    case "Synced":
      return "success";
    case "SyncFailed":
      return "error";
    case "Stale":
      return "warning";
    default:
      return "neutral";
  }
}

export default function ESOPushSecretsList() {
  const isClient = useIsClient();
  const globalNamespace = useSignalValue(selectedNamespace);
  const [items, setItems] = useState<PushSecret[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [namespace, setNamespace, namespaceRef] = useStateRef("");
  const [search, setSearch] = useState("");
  const [detected, setDetected] = useState<boolean | null>(null);

  const fetchSeq = useRef(0);
  const debounceHandle = useRef<ReturnType<typeof setTimeout> | null>(null);

  const fetchData = useCallback(async () => {
    const seq = ++fetchSeq.current;
    try {
      const ns = namespaceRef.current.trim() || undefined;
      const res = await esoApi.listPushSecrets(ns);
      if (seq !== fetchSeq.current) return;
      setItems(Array.isArray(res.data) ? res.data : []);
      setError(null);
    } catch {
      if (seq !== fetchSeq.current) return;
      setError("Failed to load PushSecrets");
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
          PushSecrets
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const byNamespace = filterByNamespace(items, globalNamespace);
  const filtered = byNamespace.filter((p) => {
    if (!search) return true;
    const q = search.toLowerCase();
    return (
      p.name.toLowerCase().includes(q) ||
      p.namespace.toLowerCase().includes(q) ||
      (p.sourceSecretName ?? "").toLowerCase().includes(q)
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
          PushSecrets
        </h1>
      </div>
      <p
        className="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        PushSecrets push Kubernetes Secrets out to a remote backend (the reverse
        direction of ExternalSecrets).
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
            htmlFor="eso-ps-ns"
          >
            Namespace
          </label>
          <input
            id="eso-ps-ns"
            type="text"
            className={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="All namespaces"
            value={namespace}
            aria-describedby="eso-ps-ns-hint"
            onChange={(e) => handleNamespaceChange(e.currentTarget.value)}
          />
          <span id="eso-ps-ns-hint" className="sr-only">
            Filter PushSecrets by namespace; updates after a brief pause.
          </span>
        </div>
        <div className="flex items-center gap-2">
          <label
            style={{
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
            }}
            htmlFor="eso-ps-search"
          >
            Search
          </label>
          <input
            id="eso-ps-search"
            type="text"
            className={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="name, source…"
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {byNamespace.length} PushSecrets
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
            { key: "status", label: "Status", width: "110px" },
            { key: "sourceSecret", label: "Source Secret", width: "1fr" },
            { key: "stores", label: "Stores", width: "70px", align: "right" },
          ]}
          rows={filtered.map((p) => {
            const detailHref = `/external-secrets/push-secrets/${encodeURIComponent(
              p.namespace,
            )}/${encodeURIComponent(p.name)}`;
            return {
              id: p.uid,
              cells: {
                name: (
                  <span className="inline-flex items-center gap-2">
                    <StatusDot status={pushToDot(p.status)} />
                    <a
                      href={detailHref}
                      className="hover:underline"
                      style={{
                        fontSize: "13px",
                        fontWeight: 500,
                        fontFamily: "var(--font-mono, monospace)",
                        color: "var(--text-primary)",
                      }}
                      onClick={(e) => e.stopPropagation()}
                    >
                      {p.name}
                    </a>
                  </span>
                ),
                namespace: (
                  <span
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {p.namespace}
                  </span>
                ),
                status: <StatusBadge status={p.status} />,
                sourceSecret: (
                  <span
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {p.sourceSecretName ?? "—"}
                  </span>
                ),
                stores: (
                  <span
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                    className="tabular-nums"
                  >
                    {(p.storeRefs ?? []).length}
                  </span>
                ),
              },
              onClick: () => {
                globalThis.location.href = detailHref;
              },
            };
          })}
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
              No PushSecrets match your filters.
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
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            No PushSecrets in this namespace. PushSecrets push Kubernetes
            Secrets back out to a source store (uncommon).
          </p>
        </div>
      )}
    </div>
  );
}
