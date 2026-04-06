import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";
import {
  ActionBadge,
  EngineBadge,
  SeverityBadge,
} from "@/components/ui/PolicyBadges.tsx";
import type { NormalizedViolation } from "@/lib/policy-types.ts";

// Irregular plurals for resource kind -> RESOURCE_DETAIL_PATHS lookup
const KIND_PLURALS: Record<string, string> = {
  ingress: "ingresses",
  endpointslice: "endpointslices",
  networkpolicy: "networkpolicies",
};

function resourceHref(
  kind: string,
  namespace?: string,
  name?: string,
): string | null {
  const lower = kind.toLowerCase();
  const plural = KIND_PLURALS[lower] ?? lower + "s";
  const basePath = RESOURCE_DETAIL_PATHS[plural];
  if (!basePath || !name) return null;
  return namespace
    ? `${basePath}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`
    : `${basePath}/${encodeURIComponent(name)}`;
}

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

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function fetchData() {
      try {
        const res = await apiGet<NormalizedViolation[]>(
          "/v1/policy/violations",
        );
        violations.value = Array.isArray(res.data) ? res.data : [];
      } catch {
        error.value = "Failed to load violations";
      }
      loading.value = false;
    }

    fetchData();
  }, []);

  if (!IS_BROWSER) return null;

  const namespaces = [
    ...new Set(
      violations.value
        .map((v) => v.namespace)
        .filter((ns): ns is string => !!ns),
    ),
  ].sort();

  const filtered = violations.value.filter((v) => {
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

  return (
    <div class="p-6">
      <h1 class="text-2xl font-bold text-text-primary mb-1">Violations</h1>
      <p class="text-sm text-text-muted mb-6">
        Policy violations across the cluster — denied, warned, and audited
        resources.
      </p>

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex-1 max-w-xs">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
            }}
            placeholder="Filter by policy, resource, message..."
          />
        </div>
        <select
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary"
          value={filterNamespace.value}
          onChange={(e) => {
            filterNamespace.value = (e.target as HTMLSelectElement).value;
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
          value={filterEngine.value}
          onChange={(e) => {
            filterEngine.value = (e.target as HTMLSelectElement).value;
          }}
        >
          <option value="all">All Engines</option>
          <option value="kyverno">Kyverno</option>
          <option value="gatekeeper">Gatekeeper</option>
        </select>
        <span class="text-xs text-text-muted">
          {filtered.length} of {violations.value.length} violations
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
              {filtered.map((v, i) => {
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
                          <a href={href} class="text-brand hover:underline">
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

      {!loading.value && !error.value && filtered.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            {violations.value.length === 0
              ? "No violations found. Your cluster is compliant!"
              : "No violations match your filters."}
          </p>
        </div>
      )}
    </div>
  );
}
