/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type { ClusterExternalSecret } from "@/lib/eso-types.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

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

const TH_STYLE = {
  fontSize: "11px",
  fontWeight: 600,
  textTransform: "uppercase" as const,
  letterSpacing: "0.05em",
  color: "var(--text-muted)",
};

export default function ESOClusterExternalSecretsList() {
  const isClient = useIsClient();
  const [items, setItems] = useState<ClusterExternalSecret[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [detected, setDetected] = useState<boolean | null>(null);

  useEffect(() => {
    const fetchData = async () => {
      try {
        const res = await esoApi.listClusterExternalSecrets();
        setItems(Array.isArray(res.data) ? res.data : []);
        setError(null);
      } catch {
        setError("Failed to load ClusterExternalSecrets");
      }
    };
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
          ClusterExternalSecrets
        </h1>
        <ESONotDetected />
      </div>
    );
  }

  const filtered = items.filter((ces) => {
    if (!search) return true;
    const q = search.toLowerCase();
    return (
      ces.name.toLowerCase().includes(q) ||
      ces.storeRef.name.toLowerCase().includes(q) ||
      (ces.targetSecretName ?? "").toLowerCase().includes(q)
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
          ClusterExternalSecrets
        </h1>
      </div>
      <p
        className="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        Cluster-scoped resources that fan out the same Secret to multiple
        namespaces.
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
            htmlFor="eso-ces-search"
          >
            Search
          </label>
          <input
            id="eso-ces-search"
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
          {filtered.length} of {items.length} ClusterExternalSecrets
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
                  Store
                </th>
                <th
                  scope="col"
                  className="px-3 py-2 text-left"
                  style={TH_STYLE}
                >
                  Target
                </th>
                <th
                  scope="col"
                  className="px-3 py-2 text-right"
                  style={TH_STYLE}
                >
                  Provisioned
                </th>
                <th
                  scope="col"
                  className="px-3 py-2 text-right"
                  style={TH_STYLE}
                >
                  Failed
                </th>
                <th scope="col" className="px-3 py-2" style={TH_STYLE} />
              </tr>
            </thead>
            <tbody>
              {filtered.map((ces) => (
                <tr
                  key={ces.uid}
                  className="hover:bg-hover/30 cursor-pointer"
                  style={{ borderTop: "1px solid var(--border-subtle)" }}
                  onClick={() => {
                    globalThis.location.href = `/external-secrets/cluster-external-secrets/${encodeURIComponent(
                      ces.name,
                    )}`;
                  }}
                >
                  <td className="px-3 py-2">
                    <span className="inline-flex items-center gap-2">
                      <StatusDot status={esoToDot(ces.status)} />
                      <a
                        href={`/external-secrets/cluster-external-secrets/${encodeURIComponent(
                          ces.name,
                        )}`}
                        className="hover:underline"
                        style={{
                          fontSize: "13px",
                          fontWeight: 500,
                          fontFamily: "var(--font-mono, monospace)",
                          color: "var(--text-primary)",
                        }}
                        onClick={(e) => e.stopPropagation()}
                      >
                        {ces.name}
                      </a>
                    </span>
                  </td>
                  <td className="px-3 py-2">
                    <StatusBadge status={ces.status} />
                  </td>
                  <td
                    className="px-3 py-2"
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {ces.storeRef.name}
                  </td>
                  <td
                    className="px-3 py-2"
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {ces.targetSecretName ?? "—"}
                  </td>
                  <td
                    className="px-3 py-2 text-right tabular-nums"
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {(ces.provisionedNamespaces ?? []).length}
                  </td>
                  <td
                    className="px-3 py-2 text-right tabular-nums"
                    style={{ fontSize: "13px", color: "var(--text-muted)" }}
                  >
                    {(ces.failedNamespaces ?? []).length}
                  </td>
                  <td
                    className="px-3 py-2 text-right"
                    style={{ color: "var(--text-muted)" }}
                  >
                    ›
                  </td>
                </tr>
              ))}
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
            No ClusterExternalSecrets match your filters.
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
            No ClusterExternalSecrets visible. These sync the same Secret to
            multiple namespaces.
          </p>
        </div>
      )}
    </div>
  );
}
