import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { stringify as yamlStringify } from "yaml";
import { meshApi } from "@/lib/mesh-api.ts";
import { ApiError } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { KindBadge, MeshBadge } from "@/components/ui/MeshBadges.tsx";
import { resourceHref } from "@/lib/k8s-links.ts";
import YamlEditor from "@/islands/YamlEditor.tsx";
import type { TrafficRoute } from "@/lib/mesh-types.ts";

export default function MeshRouteDetail({ id }: { id: string }) {
  const route = useSignal<TrafficRoute | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const notFound = useSignal(false);
  const yamlExpanded = useSignal(false);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      // meshApi.route already calls encodeURIComponent(id) internally
      const res = await meshApi.route(id);
      route.value = res.data;
      error.value = null;
      notFound.value = false;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        notFound.value = true;
        error.value = null;
        route.value = null;
      } else {
        error.value = "Failed to load route detail";
      }
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (notFound.value) {
    return (
      <div class="p-6">
        <a
          href="/networking/mesh/routing"
          class="text-sm text-brand hover:underline mb-4 inline-block"
        >
          &larr; Back to Routing
        </a>
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">Resource not found.</p>
        </div>
      </div>
    );
  }

  if (error.value || !route.value) {
    return (
      <div class="p-6">
        <a
          href="/networking/mesh/routing"
          class="text-sm text-brand hover:underline mb-4 inline-block"
        >
          &larr; Back to Routing
        </a>
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted mb-4">
            {error.value ?? "Failed to load route detail"}
          </p>
          <Button type="button" variant="ghost" onClick={handleRefresh}>
            Retry
          </Button>
        </div>
      </div>
    );
  }

  const r = route.value;
  const hosts = r.hosts ?? [];
  const gateways = r.gateways ?? [];
  const subsets = r.subsets ?? [];
  const matchers = r.matchers ?? [];
  const destinations = r.destinations ?? [];

  const rawYaml = r.raw ? yamlStringify(r.raw) : "";

  return (
    <div class="p-6">
      {/* Back link */}
      <a
        href="/networking/mesh/routing"
        class="text-sm text-brand hover:underline mb-4 inline-block"
      >
        &larr; Back to Routing
      </a>

      {/* Header */}
      <div class="flex items-center justify-between mb-6">
        <div class="flex items-center gap-3 flex-wrap">
          <h1 class="text-2xl font-bold text-text-primary">{r.name}</h1>
          <MeshBadge mesh={r.mesh} />
          <KindBadge kind={r.kind} />
        </div>
        <Button
          type="button"
          variant="ghost"
          onClick={handleRefresh}
          disabled={refreshing.value}
        >
          {refreshing.value ? "Refreshing..." : "Refresh"}
        </Button>
      </div>

      {/* Metadata panel */}
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-4 mb-6">
        <h2 class="text-sm font-medium text-text-muted mb-3">Details</h2>
        <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          <div>
            <dt class="text-text-muted">Name</dt>
            <dd class="text-text-primary">{r.name}</dd>
          </div>
          <div>
            <dt class="text-text-muted">Namespace</dt>
            <dd class="text-text-primary">{r.namespace ?? "—"}</dd>
          </div>
          <div>
            <dt class="text-text-muted">Mesh</dt>
            <dd>
              <MeshBadge mesh={r.mesh} />
            </dd>
          </div>
          <div>
            <dt class="text-text-muted">Kind</dt>
            <dd>
              <KindBadge kind={r.kind} />
            </dd>
          </div>
        </dl>
      </div>

      {/* Routing panel */}
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-4 mb-6">
        <h2 class="text-sm font-medium text-text-muted mb-3">Routing</h2>
        <dl class="grid grid-cols-1 gap-y-4 text-sm">
          <div>
            <dt class="text-text-muted mb-1">Hosts</dt>
            {hosts.length > 0
              ? (
                <dd>
                  <ul class="flex flex-col gap-1">
                    {hosts.map((h, i) => (
                      <li key={i} class="font-mono text-text-primary text-xs">
                        {h}
                      </li>
                    ))}
                  </ul>
                </dd>
              )
              : <dd class="text-text-muted">—</dd>}
          </div>
          <div>
            <dt class="text-text-muted mb-1">Gateways</dt>
            {gateways.length > 0
              ? (
                <dd>
                  <ul class="flex flex-col gap-1">
                    {gateways.map((g, i) => (
                      <li key={i} class="font-mono text-text-primary text-xs">
                        {g}
                      </li>
                    ))}
                  </ul>
                </dd>
              )
              : <dd class="text-text-muted">—</dd>}
          </div>
          <div>
            <dt class="text-text-muted mb-1">Subsets</dt>
            {subsets.length > 0
              ? (
                <dd>
                  <ul class="flex flex-wrap gap-2">
                    {subsets.map((s, i) => (
                      <li
                        key={i}
                        class="rounded px-2 py-0.5 text-xs font-mono text-text-secondary bg-surface border border-border-primary"
                      >
                        {s}
                      </li>
                    ))}
                  </ul>
                </dd>
              )
              : <dd class="text-text-muted">—</dd>}
          </div>
        </dl>
      </div>

      {/* Destinations panel */}
      {destinations.length > 0 && (
        <div class="rounded-lg border border-border-primary bg-bg-elevated p-4 mb-6">
          <h2 class="text-sm font-medium text-text-muted mb-3">
            Destinations ({destinations.length})
          </h2>
          <ul class="flex flex-col gap-3">
            {destinations.map((d, i) => {
              const host = d.host ?? "";
              const href = host
                ? resourceHref("Service", r.namespace, host)
                : null;
              return (
                <li
                  key={i}
                  class="flex flex-wrap items-center gap-3 text-sm border-b border-border-subtle last:border-0 pb-2 last:pb-0"
                >
                  <span class="text-text-muted w-5 text-right">{i + 1}.</span>
                  <span class="font-mono text-text-primary flex-1">
                    {href
                      ? (
                        <a href={href} class="text-brand hover:underline">
                          {host}
                        </a>
                      )
                      : (host || "—")}
                  </span>
                  {d.subset && (
                    <span class="text-xs text-text-muted">
                      subset:{" "}
                      <span class="text-text-secondary">{d.subset}</span>
                    </span>
                  )}
                  {d.port !== undefined && (
                    <span class="text-xs text-text-muted">
                      port: <span class="text-text-secondary">{d.port}</span>
                    </span>
                  )}
                  {d.weight !== undefined && (
                    <span class="text-xs text-text-muted">
                      weight:{" "}
                      <span class="text-text-secondary">{d.weight}</span>
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}

      {/* Matchers panel */}
      {matchers.length > 0 && (
        <div class="rounded-lg border border-border-primary bg-bg-elevated p-4 mb-6">
          <h2 class="text-sm font-medium text-text-muted mb-3">
            Matchers ({matchers.length})
          </h2>
          <ul class="flex flex-col gap-2">
            {matchers.map((m, i) => {
              const path = m.pathExact ?? m.pathPrefix ?? m.pathRegex ?? "";
              const pathKind = m.pathExact
                ? "exact"
                : m.pathPrefix
                ? "prefix"
                : m.pathRegex
                ? "regex"
                : "";
              return (
                <li
                  key={i}
                  class="text-sm flex flex-wrap items-center gap-2 border-b border-border-subtle last:border-0 pb-1 last:pb-0"
                >
                  {m.name && (
                    <span class="text-text-muted text-xs">[{m.name}]</span>
                  )}
                  {m.method && (
                    <span class="font-mono text-xs text-text-secondary px-1.5 py-0.5 rounded bg-surface border border-border-primary">
                      {m.method}
                    </span>
                  )}
                  {path && (
                    <span class="font-mono text-xs text-text-primary">
                      {pathKind && (
                        <span class="text-text-muted mr-1">{pathKind}:</span>
                      )}
                      {path}
                    </span>
                  )}
                  {!m.method && !path && (
                    <span class="text-text-muted text-xs">match all</span>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}

      {/* Raw YAML — collapsible */}
      {rawYaml && (
        <div class="rounded-lg border border-border-primary bg-bg-elevated mb-6">
          <button
            type="button"
            class="w-full flex items-center justify-between px-4 py-3 text-sm font-medium text-text-primary hover:bg-hover/20"
            onClick={() => {
              yamlExpanded.value = !yamlExpanded.value;
            }}
          >
            <span>Raw YAML</span>
            <span class="text-text-muted text-xs">
              {yamlExpanded.value ? "▲ Collapse" : "▼ Expand"}
            </span>
          </button>
          {yamlExpanded.value && (
            <div class="px-4 pb-4">
              <YamlEditor
                value={rawYaml}
                readOnly
                height="320px"
              />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
