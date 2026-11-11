import { useEffect, useState } from "react";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type { SecretStore } from "@/lib/eso-types.ts";
import SecretStoreWizard from "@/src/islands/SecretStoreWizard.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

const TH_STYLE = {
  fontSize: "11px",
  fontWeight: 600,
  textTransform: "uppercase" as const,
  letterSpacing: "0.05em",
  color: "var(--text-muted)",
};

export default function ESOClusterStoresList() {
  const isClient = useIsClient();
  const [items, setItems] = useState<SecretStore[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [detected, setDetected] = useState<boolean | null>(null);
  const [wizardOpen, setWizardOpen] = useState(false);

  useEffect(() => {
    if (!IS_BROWSER) return;
    async function fetchData() {
      try {
        const res = await esoApi.listClusterStores();
        setItems(Array.isArray(res.data) ? res.data : []);
        setError(null);
      } catch {
        setError("Failed to load ClusterSecretStores");
      }
    }
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
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    if (params.get("action") === "create") setWizardOpen(true);
  }, []);

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
          ClusterSecretStores
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const filtered = items.filter((s) => {
    if (!search) return true;
    const q = search.toLowerCase();
    return (
      s.name.toLowerCase().includes(q) ||
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
          ClusterSecretStores
        </h1>
        <button
          type="button"
          onClick={() => setWizardOpen(true)}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-opacity hover:opacity-90"
          style={{ background: "var(--accent)", color: "var(--bg-base)" }}
        >
          + New ClusterSecretStore
        </button>
      </div>
      <p
        className="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        Cluster-scoped SecretStores accessible to ExternalSecrets in any
        namespace.
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
            htmlFor="eso-cstores-search"
          >
            Search
          </label>
          <input
            id="eso-cstores-search"
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
          {filtered.length} of {items.length} ClusterSecretStores
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
        <div
          className="overflow-x-auto rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <table className="w-full">
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border-subtle)" }}>
                <th
                  scope="col"
                  className="px-3 py-2 text-left"
                  style={TH_STYLE}
                >
                  Name
                </th>
                <th
                  scope="col"
                  className="px-3 py-2 text-left"
                  style={TH_STYLE}
                >
                  Status
                </th>
                <th
                  scope="col"
                  className="px-3 py-2 text-left"
                  style={TH_STYLE}
                >
                  Provider
                </th>
                <th
                  scope="col"
                  className="px-3 py-2 text-left"
                  style={TH_STYLE}
                >
                  Ready
                </th>
                <th scope="col" className="px-3 py-2" style={TH_STYLE} />
              </tr>
            </thead>
            <tbody>
              {filtered.map((s) => {
                const detailHref = `/external-secrets/cluster-stores/${encodeURIComponent(
                  s.name,
                )}`;
                return (
                  <tr
                    key={s.uid}
                    className="hover:bg-hover/30 cursor-pointer"
                    style={{ borderTop: "1px solid var(--border-subtle)" }}
                    onClick={() => {
                      globalThis.location.href = detailHref;
                    }}
                  >
                    <td className="px-3 py-2">
                      <span className="inline-flex items-center gap-2">
                        <StatusDot status={s.ready ? "success" : "error"} />
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
                      </span>
                    </td>
                    <td className="px-3 py-2">
                      <StatusBadge status={s.status} />
                    </td>
                    <td className="px-3 py-2">
                      <ProviderBadge provider={s.provider} />
                    </td>
                    <td className="px-3 py-2">
                      {s.ready ? (
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
                      )}
                    </td>
                    <td
                      className="px-3 py-2 text-right"
                      style={{ color: "var(--text-muted)" }}
                    >
                      ›
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {!loading && !error && filtered.length === 0 && items.length > 0 && (
        <div
          className="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            No ClusterSecretStores match your filters.
          </p>
        </div>
      )}

      {!loading && !error && items.length === 0 && (
        <div
          className="text-center py-12 rounded-lg"
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            No ClusterSecretStores visible. Your permissions may restrict
            visibility, or no ClusterSecretStores exist.
          </p>
        </div>
      )}

      {wizardOpen && (
        <SecretStoreWizard
          scope="cluster"
          onClose={() => setWizardOpen(false)}
        />
      )}
    </div>
  );
}
