import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import {
  ActionBadge,
  EngineBadge,
  SeverityBadge,
} from "@/components/ui/PolicyBadges.tsx";
import { resourceHref } from "@/lib/k8s-links.ts";
import type { NormalizedViolation } from "@/lib/policy-types.ts";

const PAGE_SIZE = 100;

function getUrlParam(name: string): string {
  if (!IS_BROWSER) return "all";
  return new URLSearchParams(globalThis.location.search).get(name) ?? "all";
}

export default function ViolationBrowser() {
  const violations = useSignal<NormalizedViolation[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal(
    getUrlParam("policy") !== "all" ? getUrlParam("policy") : "",
  );
  const filterNamespace = useSignal<string>(getUrlParam("namespace"));
  const filterSeverity = useSignal<string>("all");
  const filterEngine = useSignal<string>("all");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      const res = await apiGet<NormalizedViolation[]>(
        "/v1/policies/violations",
      );
      violations.value = Array.isArray(res.data) ? res.data : [];
      error.value = null;
    } catch {
      error.value = "Failed to load violations";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchData, [
    ["violations-policyreports", "policyreports", ""],
    ["violations-clusterpolicyreports", "clusterpolicyreports", ""],
  ], 2000);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  const namespaces = [
    ...new Set(
      violations.value
        .map((v) => v.namespace)
        .filter((ns): ns is string => !!ns),
    ),
  ].sort();

  // Read the global namespace picker in the synchronous render path so the
  // signal subscription triggers a re-render when the picker changes.
  const globalNs = selectedNamespace.value;

  const filtered = violations.value.filter((v) => {
    // Global namespace picker — cluster-scoped violations (no namespace) pass
    // through regardless so they remain visible for any namespace selection.
    if (globalNs !== "all" && v.namespace && v.namespace !== globalNs) {
      return false;
    }
    if (
      filterNamespace.value !== "all" && v.namespace !== filterNamespace.value
    ) {
      return false;
    }
    if (filterSeverity.value !== "all" && v.severity !== filterSeverity.value) {
      return false;
    }
    if (filterEngine.value !== "all" && v.engine !== filterEngine.value) {
      return false;
    }
    if (search.value) {
      const q = search.value.toLowerCase();
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
  if (page.value > totalPages) page.value = totalPages;
  const displayed = filtered.slice(
    (page.value - 1) * PAGE_SIZE,
    page.value * PAGE_SIZE,
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
            value={search.value}
            onInput={(v) => {
              search.value = v;
              page.value = 1;
            }}
            placeholder="Filter by policy, resource, message..."
          />
        </div>
        <select
          style={selectStyle}
          value={filterNamespace.value}
          onChange={(e) => {
            filterNamespace.value = (e.target as HTMLSelectElement).value;
            page.value = 1;
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
          value={filterSeverity.value}
          onChange={(e) => {
            filterSeverity.value = (e.target as HTMLSelectElement).value;
            page.value = 1;
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
          value={filterEngine.value}
          onChange={(e) => {
            filterEngine.value = (e.target as HTMLSelectElement).value;
            page.value = 1;
          }}
        >
          <option value="all">All Engines</option>
          <option value="kyverno">Kyverno</option>
          <option value="gatekeeper">Gatekeeper</option>
        </select>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {violations.value.length} violations
        </span>
      </div>

      {loading.value && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "48px 0",
          }}
        >
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && (
        <p
          style={{ fontSize: "13px", color: "var(--error)", padding: "16px 0" }}
        >
          {error.value}
        </p>
      )}

      {!loading.value && !error.value && filtered.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-border-primary bg-surface">
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Policy
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Severity
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Resource
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Message
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Action
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Engine
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {displayed.map((v, i) => {
                const href = resourceHref(v.kind, v.namespace, v.name);
                return (
                  <tr
                    key={`${v.policy}-${v.name}-${i}`}
                    class="hover:bg-hover/30"
                  >
                    <td class="px-3 py-2">
                      <div class="font-medium text-text-primary">
                        {v.policy}
                      </div>
                      {v.rule && (
                        <div class="text-xs text-text-muted">{v.rule}</div>
                      )}
                    </td>
                    <td class="px-3 py-2">
                      <SeverityBadge severity={v.severity} />
                    </td>
                    <td class="px-3 py-2">
                      {href
                        ? (
                          <a
                            href={href}
                            style={{ color: "var(--accent)" }}
                            class="hover:underline"
                          >
                            {v.kind}/{v.name}
                          </a>
                        )
                        : (
                          <span class="text-text-secondary">
                            {v.kind}/{v.name}
                          </span>
                        )}
                    </td>
                    <td class="px-3 py-2 text-text-secondary">
                      {v.namespace ?? "(cluster)"}
                    </td>
                    <td class="px-3 py-2 text-text-secondary text-xs max-w-xs truncate">
                      {v.message}
                    </td>
                    <td class="px-3 py-2">
                      <ActionBadge action={v.action} />
                    </td>
                    <td class="px-3 py-2">
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
      {!loading.value && !error.value && filtered.length > PAGE_SIZE && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
            {filtered.length} violations &middot; Page {page.value} of{" "}
            {totalPages}
          </p>
          <div style={{ display: "flex", gap: "8px" }}>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                page.value--;
              }}
              disabled={page.value <= 1}
            >
              Previous
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                page.value++;
              }}
              disabled={page.value >= totalPages}
            >
              Next
            </Button>
          </div>
        </div>
      )}

      {/* Empty state — glass card */}
      {!loading.value && !error.value && filtered.length === 0 && (
        <WidgetShell>
          <div style={{ textAlign: "center", padding: "48px 24px" }}>
            <p
              style={{
                margin: 0,
                color: "var(--text-muted)",
                fontSize: "14px",
              }}
            >
              {violations.value.length === 0
                ? "No violations found. Your cluster is compliant!"
                : "No violations match your filters."}
            </p>
          </div>
        </WidgetShell>
      )}
    </div>
  );
}
