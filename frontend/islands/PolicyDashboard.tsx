import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { wsStatus } from "@/lib/ws.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import {
  BlockingBadge,
  EngineBadge,
  SeverityBadge,
} from "@/components/ui/PolicyBadges.tsx";
import type { EngineStatus, NormalizedPolicy } from "@/lib/policy-types.ts";

const PAGE_SIZE = 100;

export default function PolicyDashboard() {
  const status = useSignal<EngineStatus | null>(null);
  const policies = useSignal<NormalizedPolicy[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const filterEngine = useSignal<string>("all");
  const filterSeverity = useSignal<string>("all");
  const filterBlocking = useSignal<string>("all");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      const [statusRes, policiesRes] = await Promise.all([
        apiGet<EngineStatus>("/v1/policies/status"),
        apiGet<NormalizedPolicy[]>("/v1/policies"),
      ]);
      status.value = statusRes.data;
      policies.value = Array.isArray(policiesRes.data) ? policiesRes.data : [];
      error.value = null;
    } catch {
      error.value = "Failed to load policy data";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchData, [
    ["policy-clusterpolicies", "clusterpolicies", ""],
    ["policy-policies", "policies", ""],
  ], 2000);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  const noEngine = status.value && !status.value.detected;

  const filtered = policies.value.filter((p) => {
    if (filterEngine.value !== "all" && p.engine !== filterEngine.value) {
      return false;
    }
    if (filterSeverity.value !== "all" && p.severity !== filterSeverity.value) {
      return false;
    }
    if (filterBlocking.value !== "all") {
      const wantBlocking = filterBlocking.value === "blocking";
      if (p.blocking !== wantBlocking) return false;
    }
    if (search.value) {
      const q = search.value.toLowerCase();
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
  if (page.value > totalPages) page.value = totalPages;
  const displayed = filtered.slice(
    (page.value - 1) * PAGE_SIZE,
    page.value * PAGE_SIZE,
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
          {wsStatus.value === "connected" && (
            <span class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium text-success bg-success/10">
              <span class="w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
              Live
            </span>
          )}
          {!loading.value && (
            <>
              {!noEngine && (
                <a href="/security/create-policy">
                  <Button type="button" variant="primary">Create Policy</Button>
                </a>
              )}
              <Button
                type="button"
                variant="ghost"
                onClick={handleRefresh}
                disabled={refreshing.value}
              >
                {refreshing.value ? "Refreshing..." : "Refresh"}
              </Button>
            </>
          )}
        </div>
      </div>

      {/* Engine status banner */}
      {status.value && !noEngine && (
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
              {(status.value.detected === "kyverno" ||
                status.value.detected === "both") && (
                <EngineBadge engine="kyverno" />
              )}
              {(status.value.detected === "gatekeeper" ||
                status.value.detected === "both") && (
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
              Last checked:{" "}
              {new Date(status.value.lastChecked).toLocaleString()}
            </span>
          </div>
        </WidgetShell>
      )}

      {/* No engine state */}
      {noEngine && !loading.value && (
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
                class="hover:underline"
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
                class="hover:underline"
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
            value={search.value}
            onInput={(v) => {
              search.value = v;
              page.value = 1;
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
          style={{
            borderRadius: "9px",
            border: "1px solid var(--border-primary)",
            padding: "6px 8px",
            fontSize: "13px",
            background: "var(--bg-elevated)",
            color: "var(--text-primary)",
            cursor: "pointer",
          }}
          value={filterBlocking.value}
          onChange={(e) => {
            filterBlocking.value = (e.target as HTMLSelectElement).value;
            page.value = 1;
          }}
        >
          <option value="all">All Modes</option>
          <option value="blocking">Enforce</option>
          <option value="audit">Audit</option>
        </select>
        <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          {filtered.length} of {policies.value.length} policies
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
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
                  Name
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Engine
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Mode
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Severity
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Violations
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Targets
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Status
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {displayed.map((p) => (
                <tr key={p.id} class="hover:bg-hover/30">
                  <td class="px-3 py-2">
                    <div class="font-medium text-text-primary">{p.name}</div>
                    {p.namespace && (
                      <div class="text-xs text-text-muted">{p.namespace}</div>
                    )}
                    {p.description && (
                      <div class="text-xs text-text-muted truncate max-w-xs">
                        {p.description}
                      </div>
                    )}
                  </td>
                  <td class="px-3 py-2">
                    <EngineBadge engine={p.engine} />
                  </td>
                  <td class="px-3 py-2">
                    <BlockingBadge blocking={p.blocking} />
                  </td>
                  <td class="px-3 py-2">
                    <SeverityBadge severity={p.severity} />
                  </td>
                  <td class="px-3 py-2">
                    {p.violationCount > 0
                      ? (
                        <a
                          href={`/security/violations?policy=${
                            encodeURIComponent(p.name)
                          }`}
                          style={{ color: "var(--error)", fontWeight: 500 }}
                          class="hover:underline"
                        >
                          {p.violationCount}
                        </a>
                      )
                      : <span class="text-text-muted">0</span>}
                  </td>
                  <td class="px-3 py-2 text-text-secondary text-xs">
                    {(p.targetKinds ?? []).join(", ") || "-"}
                  </td>
                  <td class="px-3 py-2">
                    {p.ready
                      ? (
                        <span class="text-xs text-success font-medium">
                          Ready
                        </span>
                      )
                      : (
                        <span class="text-xs text-warning font-medium">
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
      {!loading.value && !error.value && filtered.length > PAGE_SIZE && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <p class="text-sm text-text-muted">
            {filtered.length} policies &middot; Page {page.value} of{" "}
            {totalPages}
          </p>
          <div class="flex gap-2">
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

      {/* Empty state */}
      {!loading.value && !error.value && filtered.length === 0 && !noEngine && (
        <WidgetShell>
          <p
            style={{
              textAlign: "center",
              color: "var(--text-muted)",
              padding: "32px 0",
              margin: 0,
            }}
          >
            {policies.value.length === 0
              ? "No policies found. Policies will appear here once defined in your cluster."
              : "No policies match your filters."}
          </p>
        </WidgetShell>
      )}
    </div>
  );
}
