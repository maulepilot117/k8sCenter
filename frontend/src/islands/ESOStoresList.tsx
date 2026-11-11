import { useCallback, useEffect, useRef, useState } from "react";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type { SecretStore } from "@/lib/eso-types.ts";
import SecretStoreWizard from "@/src/islands/SecretStoreWizard.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { filterByNamespace, selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

const NAMESPACE_DEBOUNCE_MS = 300;

/** Map SecretStore ready state → StatusDot tone. */
function storeToDot(
  status: string,
  ready: boolean,
): "success" | "error" | "warning" | "neutral" {
  if (ready) return "success";
  switch (status) {
    case "Ready":
      return "success";
    case "NotReady":
      return "error";
    default:
      return "neutral";
  }
}

export default function ESOStoresList() {
  const isClient = useIsClient();
  const nsByGlobal = useSignalValue(selectedNamespace);
  const [items, setItems] = useState<SecretStore[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // The debounced refetch reads the box through the ref, not a captured value.
  const [namespace, setNamespace, namespaceRef] = useStateRef("");
  const [search, setSearch] = useState("");
  const [detected, setDetected] = useState<boolean | null>(null);
  const [wizardOpen, setWizardOpen] = useState(false);

  const fetchSeq = useRef(0);
  const debounceHandle = useRef<ReturnType<typeof setTimeout> | null>(null);

  const fetchData = useCallback(async () => {
    const seq = ++fetchSeq.current;
    try {
      const ns = namespaceRef.current.trim() || undefined;
      const res = await esoApi.listStores(ns);
      if (seq !== fetchSeq.current) return;
      setItems(Array.isArray(res.data) ? res.data : []);
      setError(null);
    } catch {
      if (seq !== fetchSeq.current) return;
      setError("Failed to load SecretStores");
    }
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
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
    if (!IS_BROWSER) return;
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
          SecretStores
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const byNamespace = filterByNamespace(items, nsByGlobal);
  const filtered = byNamespace.filter((s) => {
    if (!search) return true;
    const q = search.toLowerCase();
    return (
      s.name.toLowerCase().includes(q) ||
      (s.namespace ?? "").toLowerCase().includes(q) ||
      (s.provider ?? "").toLowerCase().includes(q)
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
          SecretStores
        </h1>
        <button
          type="button"
          onClick={() => setWizardOpen(true)}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-opacity hover:opacity-90"
          style={{ background: "var(--accent)", color: "var(--bg-base)" }}
        >
          + New SecretStore
        </button>
      </div>
      <p
        className="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        Namespaced SecretStores describe how ESO talks to a secret backend.
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
            htmlFor="eso-stores-ns"
          >
            Namespace
          </label>
          <input
            id="eso-stores-ns"
            type="text"
            className={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="All namespaces"
            value={namespace}
            aria-describedby="eso-stores-ns-hint"
            onChange={(e) => handleNamespaceChange(e.currentTarget.value)}
          />
          <span id="eso-stores-ns-hint" className="sr-only">
            Filter SecretStores by namespace; updates after a brief pause.
          </span>
        </div>
        <div className="flex items-center gap-2">
          <label
            style={{
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
            }}
            htmlFor="eso-stores-search"
          >
            Search
          </label>
          <input
            id="eso-stores-search"
            type="text"
            className={inputStyle}
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
            }}
            placeholder="name, provider…"
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
          />
        </div>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {byNamespace.length} SecretStores
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
            { key: "status", label: "Status", width: "100px" },
            { key: "provider", label: "Provider", width: "1fr" },
            { key: "ready", label: "Ready", width: "90px" },
          ]}
          rows={filtered.map((s) => {
            const detailHref = s.namespace
              ? `/external-secrets/stores/${encodeURIComponent(s.namespace)}/${encodeURIComponent(
                  s.name,
                )}`
              : null;
            return {
              id: s.uid,
              cells: {
                name: (
                  <span className="inline-flex items-center gap-2">
                    <StatusDot status={storeToDot(s.status, s.ready)} />
                    {detailHref ? (
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
                        {s.name}
                      </a>
                    ) : (
                      <span
                        style={{
                          fontSize: "13px",
                          fontWeight: 500,
                          fontFamily: "var(--font-mono, monospace)",
                          color: "var(--text-primary)",
                        }}
                      >
                        {s.name}
                      </span>
                    )}
                  </span>
                ),
                namespace: (
                  <span
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {s.namespace ?? "—"}
                  </span>
                ),
                status: <StatusBadge status={s.status} />,
                provider: <ProviderBadge provider={s.provider} />,
                ready: s.ready ? (
                  <span
                    style={{
                      fontSize: "12px",
                      fontWeight: 500,
                      color: "var(--success)",
                    }}
                  >
                    Ready
                  </span>
                ) : (
                  <span
                    style={{
                      fontSize: "12px",
                      fontWeight: 500,
                      color: "var(--error)",
                    }}
                  >
                    Not Ready
                  </span>
                ),
              },
              onClick: detailHref
                ? () => {
                    globalThis.location.href = detailHref;
                  }
                : undefined,
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
              No SecretStores match your filters.
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
            No SecretStores in this namespace. ExternalSecrets require a
            SecretStore to function.
          </p>
        </div>
      )}

      {wizardOpen && (
        <SecretStoreWizard
          scope="namespaced"
          onClose={() => setWizardOpen(false)}
        />
      )}
    </div>
  );
}
