import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import {
  BlockingBadge,
  EngineBadge,
  SeverityBadge,
} from "@/components/ui/PolicyBadges.tsx";
import type { EngineStatus, NormalizedPolicy } from "@/lib/policy-types.ts";

export default function PolicyDashboard() {
  const status = useSignal<EngineStatus | null>(null);
  const policies = useSignal<NormalizedPolicy[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const filterEngine = useSignal<string>("all");
  const filterSeverity = useSignal<string>("all");
  const filterBlocking = useSignal<string>("all");

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function fetchData() {
      try {
        const [statusRes, policiesRes] = await Promise.all([
          apiGet<EngineStatus>("/v1/policy/status"),
          apiGet<NormalizedPolicy[]>("/v1/policy/policies"),
        ]);
        status.value = statusRes.data;
        policies.value = Array.isArray(policiesRes.data)
          ? policiesRes.data
          : [];
      } catch {
        error.value = "Failed to load policy data";
      }
      loading.value = false;
    }

    fetchData();
  }, []);

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

  return (
    <div class="p-6">
      <h1 class="text-2xl font-bold text-text-primary mb-1">Policies</h1>
      <p class="text-sm text-text-muted mb-6">
        Policy engine integration — Kyverno &amp; OPA Gatekeeper.
      </p>

      {/* Engine status banner */}
      {status.value && !noEngine && (
        <div class="mb-6 rounded-lg border border-border-primary p-4 flex items-center gap-4 bg-bg-elevated">
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium text-text-primary">
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
          <span class="text-xs text-text-muted ml-auto">
            Last checked: {new Date(status.value.lastChecked).toLocaleString()}
          </span>
        </div>
      )}

      {/* No engine state */}
      {noEngine && !loading.value && (
        <div class="mb-6 rounded-lg border border-border-primary p-6 text-center bg-bg-elevated">
          <p class="text-lg font-medium text-text-primary mb-2">
            No policy engine detected
          </p>
          <p class="text-sm text-text-muted mb-4">
            Install Kyverno or OPA Gatekeeper to enable policy management.
          </p>
          <div class="flex justify-center gap-4">
            <a
              href="https://kyverno.io/docs/installation/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-sm text-brand hover:underline"
            >
              Install Kyverno &rarr;
            </a>
            <a
              href="https://open-policy-agent.github.io/gatekeeper/website/docs/install/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-sm text-brand hover:underline"
            >
              Install Gatekeeper &rarr;
            </a>
          </div>
        </div>
      )}

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex-1 max-w-xs">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
            }}
            placeholder="Filter by name, kind, category..."
          />
        </div>
        <select
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary"
          value={filterEngine.value}
          onChange={(e) => {
            filterEngine.value = (e.target as HTMLSelectElement).value;
          }}
        >
          <option value="all">All Engines</option>
          <option value="kyverno">Kyverno</option>
          <option value="gatekeeper">Gatekeeper</option>
        </select>
        <select
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary"
          value={filterSeverity.value}
          onChange={(e) => {
            filterSeverity.value = (e.target as HTMLSelectElement).value;
          }}
        >
          <option value="all">All Severities</option>
          <option value="critical">Critical</option>
          <option value="high">High</option>
          <option value="medium">Medium</option>
          <option value="low">Low</option>
        </select>
        <select
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary"
          value={filterBlocking.value}
          onChange={(e) => {
            filterBlocking.value = (e.target as HTMLSelectElement).value;
          }}
        >
          <option value="all">All Modes</option>
          <option value="blocking">Enforce</option>
          <option value="audit">Audit</option>
        </select>
        <span class="text-xs text-text-muted">
          {filtered.length} of {policies.value.length} policies
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

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
              {filtered.map((p) => (
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
                          class="text-danger hover:underline font-medium"
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

      {!loading.value && !error.value && filtered.length === 0 &&
        !noEngine && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            {policies.value.length === 0
              ? "No policies found. Policies will appear here once defined in your cluster."
              : "No policies match your filters."}
          </p>
        </div>
      )}
    </div>
  );
}
