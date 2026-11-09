/** @jsxImportSource react */
import { useCallback, useEffect, useState } from "react";
import { stringify as yamlStringify } from "yaml";
import { Button } from "@/components/ui/Button.tsx";
import { KindBadge, MeshBadge } from "@/components/ui/MeshBadges.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError } from "@/lib/api.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import { meshApi } from "@/lib/mesh-api.ts";
import type { TrafficRoute } from "@/lib/mesh-types.ts";
import YamlEditor from "@/src/islands/YamlEditor.tsx";
import { useIsClient } from "@/src/lib/use-is-client.ts";

export default function MeshRouteDetail({ id }: { id: string }) {
  const isClient = useIsClient();
  const [route, setRoute] = useState<TrafficRoute | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [yamlExpanded, setYamlExpanded] = useState(false);
  const [refreshing, setRefreshing] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      // meshApi.route already calls encodeURIComponent(id) internally
      const res = await meshApi.route(id);
      setRoute(res.data);
      setError(null);
      setNotFound(false);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setNotFound(true);
        setError(null);
        setRoute(null);
      } else {
        setError("Failed to load route detail");
      }
    }
  }, [id]);

  useEffect(() => {
    fetchData().then(() => {
      setLoading(false);
    });
  }, [fetchData]);

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  if (!isClient) return null;

  if (loading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  if (notFound) {
    return (
      <div className="p-6">
        <a
          href="/networking/mesh/routing"
          className="text-sm text-brand hover:underline mb-4 inline-block"
        >
          &larr; Back to Routing
        </a>
        <div className="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p className="text-text-muted">Resource not found.</p>
        </div>
      </div>
    );
  }

  if (error || !route) {
    return (
      <div className="p-6">
        <a
          href="/networking/mesh/routing"
          className="text-sm text-brand hover:underline mb-4 inline-block"
        >
          &larr; Back to Routing
        </a>
        <div className="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p className="text-text-muted mb-4">
            {error ?? "Failed to load route detail"}
          </p>
          <Button type="button" variant="ghost" onClick={handleRefresh}>
            Retry
          </Button>
        </div>
      </div>
    );
  }

  const r = route;
  const hosts = r.hosts ?? [];
  const gateways = r.gateways ?? [];
  const subsets = r.subsets ?? [];
  const matchers = r.matchers ?? [];
  const destinations = r.destinations ?? [];

  const rawYaml = r.raw ? yamlStringify(r.raw) : "";

  return (
    <div className="p-6">
      {/* Back link */}
      <a
        href="/networking/mesh/routing"
        className="text-sm text-brand hover:underline mb-4 inline-block"
      >
        &larr; Back to Routing
      </a>

      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-3 flex-wrap">
          <h1 className="text-2xl font-bold text-text-primary">{r.name}</h1>
          <MeshBadge mesh={r.mesh} />
          <KindBadge kind={r.kind} />
        </div>
        <Button
          type="button"
          variant="ghost"
          onClick={handleRefresh}
          disabled={refreshing}
        >
          {refreshing ? "Refreshing..." : "Refresh"}
        </Button>
      </div>

      {/* Metadata panel */}
      <div className="rounded-lg border border-border-primary bg-elevated p-4 mb-6">
        <h2 className="text-sm font-medium text-text-muted mb-3">Details</h2>
        <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          <div>
            <dt className="text-text-muted">Name</dt>
            <dd className="text-text-primary">{r.name}</dd>
          </div>
          <div>
            <dt className="text-text-muted">Namespace</dt>
            <dd className="text-text-primary">{r.namespace ?? "—"}</dd>
          </div>
          <div>
            <dt className="text-text-muted">Mesh</dt>
            <dd>
              <MeshBadge mesh={r.mesh} />
            </dd>
          </div>
          <div>
            <dt className="text-text-muted">Kind</dt>
            <dd>
              <KindBadge kind={r.kind} />
            </dd>
          </div>
        </dl>
      </div>

      {/* Routing panel */}
      <div className="rounded-lg border border-border-primary bg-elevated p-4 mb-6">
        <h2 className="text-sm font-medium text-text-muted mb-3">Routing</h2>
        <dl className="grid grid-cols-1 gap-y-4 text-sm">
          <div>
            <dt className="text-text-muted mb-1">Hosts</dt>
            {hosts.length > 0 ? (
              <dd>
                <ul className="flex flex-col gap-1">
                  {hosts.map((h, i) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: hosts are a plain string list with no id, and the detail is replaced whole on each fetch
                    <li key={i} className="font-mono text-text-primary text-xs">
                      {h}
                    </li>
                  ))}
                </ul>
              </dd>
            ) : (
              <dd className="text-text-muted">—</dd>
            )}
          </div>
          <div>
            <dt className="text-text-muted mb-1">Gateways</dt>
            {gateways.length > 0 ? (
              <dd>
                <ul className="flex flex-col gap-1">
                  {gateways.map((g, i) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: gateways are a plain string list with no id, and the detail is replaced whole on each fetch
                    <li key={i} className="font-mono text-text-primary text-xs">
                      {g}
                    </li>
                  ))}
                </ul>
              </dd>
            ) : (
              <dd className="text-text-muted">—</dd>
            )}
          </div>
          <div>
            <dt className="text-text-muted mb-1">Subsets</dt>
            {subsets.length > 0 ? (
              <dd>
                <ul className="flex flex-wrap gap-2">
                  {subsets.map((s, i) => (
                    <li
                      // biome-ignore lint/suspicious/noArrayIndexKey: subsets are a plain string list with no id, and the detail is replaced whole on each fetch
                      key={i}
                      className="rounded px-2 py-0.5 text-xs font-mono text-text-secondary bg-surface border border-border-primary"
                    >
                      {s}
                    </li>
                  ))}
                </ul>
              </dd>
            ) : (
              <dd className="text-text-muted">—</dd>
            )}
          </div>
        </dl>
      </div>

      {/* Destinations panel */}
      {destinations.length > 0 && (
        <div className="rounded-lg border border-border-primary bg-elevated p-4 mb-6">
          <h2 className="text-sm font-medium text-text-muted mb-3">
            Destinations ({destinations.length})
          </h2>
          <ul className="flex flex-col gap-3">
            {destinations.map((d, i) => {
              const host = d.host ?? "";
              const href = host
                ? resourceHref("Service", r.namespace, host)
                : null;
              return (
                <li
                  // biome-ignore lint/suspicious/noArrayIndexKey: destinations are positional entries with no id, and the detail is replaced whole on each fetch
                  key={i}
                  className="flex flex-wrap items-center gap-3 text-sm border-b border-border-subtle last:border-0 pb-2 last:pb-0"
                >
                  <span className="text-text-muted w-5 text-right">
                    {i + 1}.
                  </span>
                  <span className="font-mono text-text-primary flex-1">
                    {href ? (
                      <a href={href} className="text-brand hover:underline">
                        {host}
                      </a>
                    ) : (
                      host || "—"
                    )}
                  </span>
                  {d.subset && (
                    <span className="text-xs text-text-muted">
                      subset:{" "}
                      <span className="text-text-secondary">{d.subset}</span>
                    </span>
                  )}
                  {d.port !== undefined && (
                    <span className="text-xs text-text-muted">
                      port:{" "}
                      <span className="text-text-secondary">{d.port}</span>
                    </span>
                  )}
                  {d.weight !== undefined && (
                    <span className="text-xs text-text-muted">
                      weight:{" "}
                      <span className="text-text-secondary">{d.weight}</span>
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
        <div className="rounded-lg border border-border-primary bg-elevated p-4 mb-6">
          <h2 className="text-sm font-medium text-text-muted mb-3">
            Matchers ({matchers.length})
          </h2>
          <ul className="flex flex-col gap-2">
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
                  // biome-ignore lint/suspicious/noArrayIndexKey: matchers are positional entries with no id, and the detail is replaced whole on each fetch
                  key={i}
                  className="text-sm flex flex-wrap items-center gap-2 border-b border-border-subtle last:border-0 pb-1 last:pb-0"
                >
                  {m.name && (
                    <span className="text-text-muted text-xs">[{m.name}]</span>
                  )}
                  {m.method && (
                    <span className="font-mono text-xs text-text-secondary px-1.5 py-0.5 rounded bg-surface border border-border-primary">
                      {m.method}
                    </span>
                  )}
                  {path && (
                    <span className="font-mono text-xs text-text-primary">
                      {pathKind && (
                        <span className="text-text-muted mr-1">
                          {pathKind}:
                        </span>
                      )}
                      {path}
                    </span>
                  )}
                  {!m.method && !path && (
                    <span className="text-text-muted text-xs">match all</span>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}

      {/* Raw YAML — collapsible */}
      {rawYaml && (
        <div className="rounded-lg border border-border-primary bg-elevated mb-6">
          <button
            type="button"
            className="w-full flex items-center justify-between px-4 py-3 text-sm font-medium text-text-primary hover:bg-hover/20"
            onClick={() => {
              setYamlExpanded((v) => !v);
            }}
          >
            <span>Raw YAML</span>
            <span className="text-text-muted text-xs">
              {yamlExpanded ? "▲ Collapse" : "▼ Expand"}
            </span>
          </button>
          {yamlExpanded && (
            <div className="px-4 pb-4">
              <YamlEditor value={rawYaml} readOnly height="320px" />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
