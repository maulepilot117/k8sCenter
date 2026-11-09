/** @jsxImportSource react */
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { LiveBadge } from "@/components/ui/LiveBadge.tsx";
import {
  BlockingBadge,
  EngineBadge,
  SeverityBadge,
} from "@/components/ui/PolicyBadges.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { apiGet } from "@/lib/api.ts";
import type { EngineStatus, NormalizedPolicy } from "@/lib/policy-types.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

const PAGE_SIZE = 100;

export default function PolicyDashboard() {
  const [status, setStatus] = useState<EngineStatus | null>(null);
  const [policies, setPolicies] = useState<NormalizedPolicy[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [filterEngine, setFilterEngine] = useState<string>("all");
  const [filterSeverity, setFilterSeverity] = useState<string>("all");
  const [filterBlocking, setFilterBlocking] = useState<string>("all");
  const [page, setPage] = useState(1);
  const [refreshing, setRefreshing] = useState(false);
  const isClient = useIsClient();

  const fetchData = useCallback(async () => {
    try {
      const [statusRes, policiesRes] = await Promise.all([
        apiGet<EngineStatus>("/v1/policies/status"),
        apiGet<NormalizedPolicy[]>("/v1/policies"),
      ]);
      setStatus(statusRes.data);
      setPolicies(Array.isArray(policiesRes.data) ? policiesRes.data : []);
      setError(null);
    } catch {
      setError("Failed to load policy data");
    }
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      setLoading(false);
    });
  }, [fetchData]);

  useWsRefetch(
    fetchData,
    [
      ["policy-clusterpolicies", "clusterpolicies", ""],
      ["policy-policies", "policies", ""],
    ],
    2000,
  );

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  if (!isClient) return null;

  const noEngine = status && !status.detected;

  const filtered = policies.filter((p) => {
    if (filterEngine !== "all" && p.engine !== filterEngine) {
      return false;
    }
    if (filterSeverity !== "all" && p.severity !== filterSeverity) {
      return false;
    }
    if (filterBlocking !== "all") {
      const wantBlocking = filterBlocking === "blocking";
      if (p.blocking !== wantBlocking) return false;
    }
    if (search) {
      const q = search.toLowerCase();
      return (
        p.name.toLowerCase().includes(q) ||
        p.kind.toLowerCase().includes(q) ||
        (p.category ?? "").toLowerCase().includes(q) ||
        (p.description ?? "").toLowerCase().includes(q)
      );
    }
    return true;
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  const currentPage = Math.min(page, totalPages);
  // Keep the stored page in range as well, so a list that shrinks and grows
  // back stays on the clamped page instead of returning to the old one.
  if (page > totalPages) setPage(totalPages);
  const displayed = filtered.slice(
    (currentPage - 1) * PAGE_SIZE,
    currentPage * PAGE_SIZE,
  );

  return (
    <div
      style={{
        padding: "24px",
        display: "flex",
        flexDirection: "column",
        gap: "20px",
      }}
    >
      {/* Page header */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: "16px",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              lineHeight: 1.2,
            }}
          >
            Policies
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Policy engine integration — Kyverno &amp; OPA Gatekeeper.
          </p>
        </div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "8px",
            flexShrink: 0,
          }}
        >
          <LiveBadge />
          {!loading && (
            <>
              {!noEngine && (
                <a href="/security/create-policy">
                  <Button type="button" variant="primary">
                    Create Policy
                  </Button>
                </a>
              )}
              <Button
                type="button"
                variant="ghost"
                onClick={handleRefresh}
                disabled={refreshing}
              >
                {refreshing ? "Refreshing..." : "Refresh"}
              </Button>
            </>
          )}
        </div>
      </div>

      {/* Engine status banner */}
      {status && !noEngine && (
        <WidgetShell title="Policy Engines">
          <div style={{ display: "flex", alignItems: "center", gap: "16px" }}>
            <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
              <span
                style={{
                  fontSize: "13px",
                  fontWeight: 500,
                  color: "var(--text-primary)",
                }}
              >
                Engines detected:
              </span>
              {(status.detected === "kyverno" ||
                status.detected === "both") && <EngineBadge engine="kyverno" />}
              {(status.detected === "gatekeeper" ||
                status.detected === "both") && (
                <EngineBadge engine="gatekeeper" />
              )}
            </div>
            <span
              style={{
                fontSize: "12px",
                color: "var(--text-muted)",
                marginLeft: "auto",
              }}
            >
              Last checked: {new Date(status.lastChecked).toLocaleString()}
            </span>
          </div>
        </WidgetShell>
      )}

      {/* No engine state */}
      {noEngine && !loading && (
        <WidgetShell title="Policy Engines">
          <div style={{ textAlign: "center", padding: "16px 0" }}>
            <p
              style={{
                fontSize: "16px",
                fontWeight: 500,
                color: "var(--text-primary)",
                margin: "0 0 8px",
              }}
            >
              No policy engine detected
            </p>
            <p
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                margin: "0 0 16px",
              }}
            >
              Install Kyverno or OPA Gatekeeper to enable policy management.
            </p>
            <div
              style={{ display: "flex", justifyContent: "center", gap: "16px" }}
            >
              <a
                href="https://kyverno.io/docs/installation/"
                target="_blank"
                rel="noopener noreferrer"
                style={{
                  fontSize: "13px",
                  color: "var(--accent)",
                  textDecoration: "none",
                }}
                className="hover:underline"
              >
                Install Kyverno &rarr;
              </a>
              <a
                href="https://open-policy-agent.github.io/gatekeeper/website/docs/install/"
                target="_blank"
                rel="noopener noreferrer"
                style={{
                  fontSize: "13px",
                  color: "var(--accent)",
                  textDecoration: "none",
                }}
                className="hover:underline"
              >
                Install Gatekeeper &rarr;
              </a>
            </div>
          </div>
        </WidgetShell>
      )}

      {/* Filters */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: "16px",
        }}
      >
        <div style={{ flex: 1, maxWidth: "320px" }}>
          <SearchBar
            value={search}
            onInput={(v) => {
              setSearch(v);
              setPage(1);
            }}
            placeholder="Filter by name, kind, category..."
          />
        </div>
        <select
          style={{
            borderRadius: "9px",
            border: "1px solid var(--border-primary)",
            padding: "6px 8px",
            fontSize: "13px",
            background: "var(--bg-elevated)",
            color: "var(--text-primary)",
            cursor: "pointer",
          }}
          value={filterEngine}
          onChange={(e) => {
            setFilterEngine(e.currentTarget.value);
            setPage(1);
          }}
        >
          <option value="all">All Engines</option>
          <option value="kyverno">Kyverno</option>
          <option value="gatekeeper">Gatekeeper</option>
        </select>
        <select
          style={{
            borderRadius: "9px",
            border: "1px solid var(--border-primary)",
            padding: "6px 8px",
            fontSize: "13px",
            background: "var(--bg-elevated)",
            color: "var(--text-primary)",
            cursor: "pointer",
          }}
          value={filterSeverity}
          onChange={(e) => {
            setFilterSeverity(e.currentTarget.value);
            setPage(1);
          }}
        >
          <option value="all">All Severities</option>
          <option value="critical">Critical</option>
          <option value="high">High</option>
          <option value="medium">Medium</option>
          <option value="low">Low</option>
        </select>
        <select
          style={{
            borderRadius: "9px",
            border: "1px solid var(--border-primary)",
            padding: "6px 8px",
            fontSize: "13px",
            background: "var(--bg-elevated)",
            color: "var(--text-primary)",
            cursor: "pointer",
          }}
          value={filterBlocking}
          onChange={(e) => {
            setFilterBlocking(e.currentTarget.value);
            setPage(1);
          }}
        >
          <option value="all">All Modes</option>
          <option value="blocking">Enforce</option>
          <option value="audit">Audit</option>
        </select>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {policies.length} policies
        </span>
      </div>

      {loading && (
        <div className="flex justify-center py-12">
          <Spinner className="text-brand" />
        </div>
      )}

      {error && (
        <p
          style={{ fontSize: "13px", color: "var(--error)", padding: "16px 0" }}
        >
          {error}
        </p>
      )}

      {!loading && !error && filtered.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-border-primary">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border-primary bg-surface">
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Name
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Engine
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Mode
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Severity
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Violations
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Targets
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Status
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border-subtle">
              {displayed.map((p) => (
                <tr key={p.id} className="hover:bg-hover/30">
                  <td className="px-3 py-2">
                    <div className="font-medium text-text-primary">
                      {p.name}
                    </div>
                    {p.namespace && (
                      <div className="text-xs text-text-muted">
                        {p.namespace}
                      </div>
                    )}
                    {p.description && (
                      <div className="text-xs text-text-muted truncate max-w-xs">
                        {p.description}
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-2">
                    <EngineBadge engine={p.engine} />
                  </td>
                  <td className="px-3 py-2">
                    <BlockingBadge blocking={p.blocking} />
                  </td>
                  <td className="px-3 py-2">
                    <SeverityBadge severity={p.severity} />
                  </td>
                  <td className="px-3 py-2">
                    {p.violationCount > 0 ? (
                      <a
                        href={`/security/violations?policy=${encodeURIComponent(
                          p.name,
                        )}`}
                        style={{ color: "var(--error)", fontWeight: 500 }}
                        className="hover:underline"
                      >
                        {p.violationCount}
                      </a>
                    ) : (
                      <span className="text-text-muted">0</span>
                    )}
                  </td>
                  <td className="px-3 py-2 text-text-secondary text-xs">
                    {(p.targetKinds ?? []).join(", ") || "-"}
                  </td>
                  <td className="px-3 py-2">
                    {p.ready ? (
                      <span className="text-xs text-success font-medium">
                        Ready
                      </span>
                    ) : (
                      <span className="text-xs text-warning font-medium">
                        Not Ready
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {!loading && !error && filtered.length > PAGE_SIZE && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <p className="text-sm text-text-muted">
            {filtered.length} policies &middot; Page {currentPage} of{" "}
            {totalPages}
          </p>
          <div className="flex gap-2">
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setPage(currentPage - 1);
              }}
              disabled={currentPage <= 1}
            >
              Previous
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setPage(currentPage + 1);
              }}
              disabled={currentPage >= totalPages}
            >
              Next
            </Button>
          </div>
        </div>
      )}

      {/* Empty state */}
      {!loading && !error && filtered.length === 0 && !noEngine && (
        <WidgetShell>
          <p
            style={{
              textAlign: "center",
              color: "var(--text-muted)",
              padding: "32px 0",
              margin: 0,
            }}
          >
            {policies.length === 0
              ? "No policies found. Policies will appear here once defined in your cluster."
              : "No policies match your filters."}
          </p>
        </WidgetShell>
      )}
    </div>
  );
}
