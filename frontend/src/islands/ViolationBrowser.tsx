/** @jsxImportSource react */
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { RemoteRefreshHint } from "@/components/ui/LiveBadge.tsx";
import {
  ActionBadge,
  EngineBadge,
  SeverityBadge,
} from "@/components/ui/PolicyBadges.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { apiGet } from "@/lib/api.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import type { NormalizedViolation } from "@/lib/policy-types.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

const PAGE_SIZE = 100;

function getUrlParam(name: string): string {
  if (!IS_BROWSER) return "all";
  return new URLSearchParams(globalThis.location.search).get(name) ?? "all";
}

export default function ViolationBrowser() {
  const [violations, setViolations] = useState<NormalizedViolation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState(() =>
    getUrlParam("policy") !== "all" ? getUrlParam("policy") : "",
  );
  const [filterSeverity, setFilterSeverity] = useState<string>("all");
  const [filterEngine, setFilterEngine] = useState<string>("all");
  const [page, setPage] = useState(1);
  const [refreshing, setRefreshing] = useState(false);
  const isClient = useIsClient();
  const globalNs = useSignalValue(selectedNamespace);

  const fetchData = useCallback(async () => {
    try {
      const res = await apiGet<NormalizedViolation[]>(
        "/v1/policies/violations",
      );
      setViolations(Array.isArray(res.data) ? res.data : []);
      setError(null);
    } catch {
      setError("Failed to load violations");
    }
  }, []);

  // The namespace filter is the global picker, so it reads the same here as
  // in the top bar. A link such as the compliance page's ?namespace= selects
  // that namespace on arrival.
  useEffect(() => {
    if (!IS_BROWSER) return;
    const linked = getUrlParam("namespace");
    if (linked !== "all") selectedNamespace.value = linked;
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
      ["violations-policyreports", "policyreports", ""],
      ["violations-clusterpolicyreports", "clusterpolicyreports", ""],
    ],
    2000,
  );

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  if (!isClient) return null;

  // The selected namespace stays an option even with no violations in it, so
  // the select never renders blank.
  const namespaces = [
    ...new Set([
      ...violations.map((v) => v.namespace).filter((ns): ns is string => !!ns),
      ...(globalNs !== "all" ? [globalNs] : []),
    ]),
  ].sort();

  const filtered = violations.filter((v) => {
    // Global namespace picker — cluster-scoped violations (no namespace) pass
    // through regardless so they remain visible for any namespace selection.
    if (globalNs !== "all" && v.namespace && v.namespace !== globalNs) {
      return false;
    }
    if (filterSeverity !== "all" && v.severity !== filterSeverity) {
      return false;
    }
    if (filterEngine !== "all" && v.engine !== filterEngine) {
      return false;
    }
    if (search) {
      const q = search.toLowerCase();
      return (
        v.policy.toLowerCase().includes(q) ||
        v.name.toLowerCase().includes(q) ||
        v.kind.toLowerCase().includes(q) ||
        v.message.toLowerCase().includes(q)
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

  const selectStyle = {
    borderRadius: "9px",
    border: "1px solid var(--border-primary)",
    background: "var(--bg-elevated)",
    color: "var(--text-primary)",
    padding: "6px 10px",
    fontSize: "13px",
    outline: "none",
  };

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
            Violations
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Policy violations across the cluster — denied, warned, and audited
            resources.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <RemoteRefreshHint />
          {!loading && (
            <Button
              type="button"
              variant="ghost"
              onClick={handleRefresh}
              disabled={refreshing}
            >
              {refreshing ? "Refreshing..." : "Refresh"}
            </Button>
          )}
        </div>
      </div>

      {/* Filters */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: "12px",
        }}
      >
        <div style={{ flex: 1, maxWidth: "320px" }}>
          <SearchBar
            value={search}
            onInput={(v) => {
              setSearch(v);
              setPage(1);
            }}
            placeholder="Filter by policy, resource, message..."
          />
        </div>
        <select
          style={selectStyle}
          value={globalNs}
          onChange={(e) => {
            selectedNamespace.value = e.currentTarget.value;
            setPage(1);
          }}
        >
          <option value="all">All Namespaces</option>
          {namespaces.map((ns) => (
            <option key={ns} value={ns}>
              {ns}
            </option>
          ))}
        </select>
        <select
          style={selectStyle}
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
          style={selectStyle}
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
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {violations.length} violations
        </span>
      </div>

      {loading && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "48px 0",
          }}
        >
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
                  Policy
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Severity
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Resource
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Message
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Action
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Engine
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border-subtle">
              {displayed.map((v, i) => {
                const href = resourceHref(v.kind, v.namespace, v.name);
                return (
                  <tr
                    // biome-ignore lint/suspicious/noArrayIndexKey: a violation has no unique id, and the same policy can hit one resource name twice
                    key={`${v.policy}-${v.name}-${i}`}
                    className="hover:bg-hover/30"
                  >
                    <td className="px-3 py-2">
                      <div className="font-medium text-text-primary">
                        {v.policy}
                      </div>
                      {v.rule && (
                        <div className="text-xs text-text-muted">{v.rule}</div>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <SeverityBadge severity={v.severity} />
                    </td>
                    <td className="px-3 py-2">
                      {href ? (
                        <a
                          href={href}
                          style={{ color: "var(--accent)" }}
                          className="hover:underline"
                        >
                          {v.kind}/{v.name}
                        </a>
                      ) : (
                        <span className="text-text-secondary">
                          {v.kind}/{v.name}
                        </span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
                      {v.namespace ?? "(cluster)"}
                    </td>
                    <td className="px-3 py-2 text-text-secondary text-xs max-w-xs truncate">
                      {v.message}
                    </td>
                    <td className="px-3 py-2">
                      <ActionBadge action={v.action} />
                    </td>
                    <td className="px-3 py-2">
                      <EngineBadge engine={v.engine} />
                    </td>
                  </tr>
                );
              })}
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
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            {filtered.length} violations &middot; Page {currentPage} of{" "}
            {totalPages}
          </p>
          <div style={{ display: "flex", gap: "8px" }}>
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

      {/* Empty state — glass card */}
      {!loading && !error && filtered.length === 0 && (
        <WidgetShell>
          <div style={{ textAlign: "center", padding: "48px 24px" }}>
            <p
              style={{
                margin: 0,
                color: "var(--text-muted)",
                fontSize: "14px",
              }}
            >
              {violations.length === 0
                ? "No violations found. Your cluster is compliant!"
                : "No violations match your filters."}
            </p>
          </div>
        </WidgetShell>
      )}
    </div>
  );
}
