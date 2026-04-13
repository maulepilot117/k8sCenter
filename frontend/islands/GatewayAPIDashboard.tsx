import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { useSignal } from "@preact/signals";
import { apiGet } from "@/lib/api.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type {
  GatewayAPIStatus,
  GatewayAPISummary,
  GatewayClassSummary,
  GatewayListData,
  GatewayResourceKind,
  GatewaySummary,
  HTTPRouteSummary,
  KindSummary,
  RouteSummary,
} from "@/lib/gateway-types.ts";

const KIND_LABELS: Record<GatewayResourceKind, string> = {
  gatewayclasses: "Gateway Classes",
  gateways: "Gateways",
  httproutes: "HTTP Routes",
  grpcroutes: "gRPC Routes",
  tcproutes: "TCP Routes",
  tlsroutes: "TLS Routes",
  udproutes: "UDP Routes",
};

const SUMMARY_KEYS: Record<GatewayResourceKind, keyof GatewayAPISummary> = {
  gatewayclasses: "gatewayClasses",
  gateways: "gateways",
  httproutes: "httpRoutes",
  grpcroutes: "grpcRoutes",
  tcproutes: "tcpRoutes",
  tlsroutes: "tlsRoutes",
  udproutes: "udpRoutes",
};

function formatAge(ageStr: string): string {
  if (!ageStr) return "\u2014";
  const date = new Date(ageStr);
  if (isNaN(date.getTime())) {
    // Already a relative string like "2d" from the backend
    return ageStr;
  }
  const now = Date.now();
  const diffMs = now - date.getTime();
  if (diffMs < 0) return "0s";
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

function getAcceptedStatus(
  conditions?: { type: string; status: string }[],
): boolean {
  if (!conditions || conditions.length === 0) return false;
  const accepted = conditions.find(
    (c) => c.type === "Accepted" || c.type === "Programmed",
  );
  return accepted?.status === "True";
}

function StatusBadge(
  { conditions }: { conditions?: { type: string; status: string }[] },
) {
  const ok = getAcceptedStatus(conditions);
  return (
    <span
      class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
        ok ? "bg-success/15 text-success" : "bg-danger/15 text-danger"
      }`}
    >
      {ok ? "Accepted" : "Not Ready"}
    </span>
  );
}

export default function GatewayAPIDashboard() {
  const status = useSignal<GatewayAPIStatus | null>(null);
  const summary = useSignal<GatewayAPISummary | null>(null);
  const loading = useSignal(true);
  const error = useSignal("");
  const activeKind = useSignal<GatewayResourceKind | null>(null);
  const listData = useSignal<GatewayListData | null>(null);
  const listLoading = useSignal(false);
  const search = useSignal("");

  async function fetchList(kind: GatewayResourceKind) {
    listLoading.value = true;
    try {
      let endpoint: string;
      switch (kind) {
        case "gatewayclasses":
          endpoint = "/v1/gateway/gatewayclasses";
          break;
        case "gateways":
          endpoint = "/v1/gateway/gateways";
          break;
        case "httproutes":
          endpoint = "/v1/gateway/httproutes";
          break;
        default:
          endpoint = `/v1/gateway/routes?kind=${kind}`;
          break;
      }
      const res = await apiGet<unknown[]>(endpoint);
      const items = Array.isArray(res.data) ? res.data : [];
      // deno-lint-ignore no-explicit-any
      listData.value = { kind, items } as any;
    } catch {
      listData.value = { kind, items: [] } as GatewayListData;
    }
    listLoading.value = false;
  }

  function handleCardClick(kind: GatewayResourceKind) {
    activeKind.value = kind;
    search.value = "";
    fetchList(kind);
  }

  // URL state sync
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (activeKind.value) {
      const url = new URL(globalThis.location.href);
      url.searchParams.set("kind", activeKind.value);
      globalThis.history.replaceState(null, "", url.toString());
    } else {
      const url = new URL(globalThis.location.href);
      url.searchParams.delete("kind");
      globalThis.history.replaceState(null, "", url.toString());
    }
  }, [activeKind.value]);

  // Initial load
  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    const kindParam = params.get("kind") as GatewayResourceKind | null;

    Promise.all([
      apiGet<GatewayAPIStatus>("/v1/gateway/status"),
      apiGet<GatewayAPISummary>("/v1/gateway/summary"),
    ]).then(([statusRes, summaryRes]) => {
      status.value = statusRes.data ?? null;
      summary.value = summaryRes.data ?? null;

      if (
        kindParam && status.value?.available &&
        Object.keys(KIND_LABELS).includes(kindParam)
      ) {
        activeKind.value = kindParam;
        fetchList(kindParam);
      }
      loading.value = false;
    }).catch(() => {
      error.value = "Failed to load Gateway API status";
      loading.value = false;
    });
  }, []);

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (error.value) {
    return <p class="text-sm text-danger py-4">{error.value}</p>;
  }

  if (!status.value?.available) {
    return (
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-8 text-center">
        <h3 class="text-lg font-medium text-text-primary">
          Gateway API Not Detected
        </h3>
        <p class="mt-2 text-sm text-text-muted">
          No Gateway API CRDs found in this cluster. Install Gateway API to
          manage Gateways, HTTPRoutes, and other resources.
        </p>
      </div>
    );
  }

  // List mode
  if (activeKind.value) {
    return (
      <div>
        <button
          type="button"
          onClick={() => {
            activeKind.value = null;
            listData.value = null;
            search.value = "";
          }}
          class="mb-4 inline-flex items-center gap-1 text-sm text-text-muted hover:text-text-primary transition-colors"
        >
          <svg
            class="h-4 w-4"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M17 10a.75.75 0 01-.75.75H5.612l4.158 3.96a.75.75 0 11-1.04 1.08l-5.5-5.25a.75.75 0 010-1.08l5.5-5.25a.75.75 0 111.04 1.08L5.612 9.25H16.25A.75.75 0 0117 10z"
              clip-rule="evenodd"
            />
          </svg>
          Back to overview
        </button>

        <h2 class="text-xl font-semibold text-text-primary mb-4">
          {KIND_LABELS[activeKind.value]}
        </h2>

        <div class="mb-4 flex flex-wrap items-center gap-4">
          <div class="flex-1 max-w-xs">
            <SearchBar
              value={search.value}
              onInput={(v) => {
                search.value = v;
              }}
              placeholder="Filter by name or namespace..."
            />
          </div>
          {listData.value && (
            <span class="text-xs text-text-muted">
              {listData.value.items.length} total
            </span>
          )}
        </div>

        {listLoading.value && (
          <div class="flex justify-center py-12">
            <Spinner class="text-brand" />
          </div>
        )}

        {!listLoading.value && listData.value &&
          renderTable(activeKind.value, listData.value, search.value)}
      </div>
    );
  }

  // Overview mode
  const installedKinds = (status.value.installedKinds ?? []) as string[];
  const allKinds = Object.keys(KIND_LABELS) as GatewayResourceKind[];
  const visibleKinds = allKinds.filter((k) => installedKinds.includes(k));

  if (visibleKinds.length === 0) {
    return (
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-8 text-center">
        <h3 class="text-lg font-medium text-text-primary">
          No Gateway API Resources
        </h3>
        <p class="mt-2 text-sm text-text-muted">
          Gateway API CRDs are installed but no resource kinds were detected.
        </p>
      </div>
    );
  }

  return (
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {visibleKinds.map((kind) => {
        const kindSummary: KindSummary = summary.value
          ? summary.value[SUMMARY_KEYS[kind]]
          : { total: 0, healthy: 0, degraded: 0 };
        return (
          <button
            type="button"
            key={kind}
            onClick={() => handleCardClick(kind)}
            class="rounded-lg border border-border-primary bg-bg-elevated p-5 text-left transition-colors hover:border-border-hover hover:bg-hover/30"
          >
            <div class="text-sm font-medium text-text-muted">
              {KIND_LABELS[kind]}
            </div>
            <div class="mt-1 text-2xl font-semibold text-text-primary">
              {kindSummary.total}
            </div>
            <div class="mt-1 text-xs">
              {kindSummary.healthy > 0 && (
                <span class="text-success">
                  {kindSummary.healthy} healthy
                </span>
              )}
              {kindSummary.degraded > 0 && (
                <span class="text-danger ml-2">
                  {kindSummary.degraded} degraded
                </span>
              )}
              {kindSummary.healthy === 0 && kindSummary.degraded === 0 &&
                kindSummary.total > 0 && (
                <span class="text-text-muted">
                  {kindSummary.total} total
                </span>
              )}
            </div>
          </button>
        );
      })}
    </div>
  );
}

function filterBySearch<
  T extends { name: string; namespace?: string },
>(items: T[], query: string): T[] {
  if (!query) return items;
  const q = query.toLowerCase();
  return items.filter(
    (item) =>
      item.name.toLowerCase().includes(q) ||
      (item.namespace && item.namespace.toLowerCase().includes(q)),
  );
}

function getDetailHref(
  kind: GatewayResourceKind,
  item: { name: string; namespace?: string },
): string {
  switch (kind) {
    case "gatewayclasses":
      return `/networking/gateway-api/gatewayclasses/${item.name}`;
    case "gateways":
      return `/networking/gateway-api/gateways/${item.namespace}/${item.name}`;
    case "httproutes":
      return `/networking/gateway-api/httproutes/${item.namespace}/${item.name}`;
    default:
      return `/networking/gateway-api/${kind}/${item.namespace}/${item.name}`;
  }
}

const TH = "px-3 py-2 text-left text-xs font-medium text-text-muted";
const TD = "px-3 py-2";
const TD_SEC = "px-3 py-2 text-text-secondary";

function renderTable(
  kind: GatewayResourceKind,
  data: GatewayListData,
  query: string,
) {
  switch (kind) {
    case "gatewayclasses":
      return renderGatewayClassesTable(
        filterBySearch(
          data.items as GatewayClassSummary[],
          query,
        ),
      );
    case "gateways":
      return renderGatewaysTable(
        filterBySearch(data.items as GatewaySummary[], query),
      );
    case "httproutes":
      return renderHTTPRoutesTable(
        filterBySearch(
          data.items as HTTPRouteSummary[],
          query,
        ),
      );
    default:
      return renderRoutesTable(
        kind,
        filterBySearch(data.items as RouteSummary[], query),
      );
  }
}

function EmptyTable({ message }: { message: string }) {
  return (
    <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
      <p class="text-text-muted">{message}</p>
    </div>
  );
}

function renderGatewayClassesTable(items: GatewayClassSummary[]) {
  if (items.length === 0) {
    return <EmptyTable message="No gateway classes found." />;
  }
  return (
    <div class="overflow-x-auto rounded-lg border border-border-primary">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border-primary bg-surface">
            <th class={TH}>Name</th>
            <th class={TH}>Controller</th>
            <th class={TH}>Description</th>
            <th class={TH}>Status</th>
            <th class={TH}>Age</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border-subtle">
          {items.map((item) => (
            <tr
              key={item.name}
              class="hover:bg-hover/30 cursor-pointer"
              onClick={() => {
                globalThis.location.href = getDetailHref(
                  "gatewayclasses",
                  item,
                );
              }}
            >
              <td class={TD}>
                <a
                  href={getDetailHref("gatewayclasses", item)}
                  class="font-medium text-brand hover:underline"
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.name}
                </a>
              </td>
              <td class={TD_SEC}>{item.controllerName}</td>
              <td class={`${TD_SEC} max-w-[200px] truncate`}>
                {item.description || "\u2014"}
              </td>
              <td class={TD}>
                <StatusBadge conditions={item.conditions} />
              </td>
              <td class={TD_SEC}>{formatAge(item.age)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function renderGatewaysTable(items: GatewaySummary[]) {
  if (items.length === 0) {
    return <EmptyTable message="No gateways found." />;
  }
  return (
    <div class="overflow-x-auto rounded-lg border border-border-primary">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border-primary bg-surface">
            <th class={TH}>Name</th>
            <th class={TH}>Namespace</th>
            <th class={TH}>Class</th>
            <th class={TH}>Listeners</th>
            <th class={TH}>Addresses</th>
            <th class={TH}>Status</th>
            <th class={TH}>Age</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border-subtle">
          {items.map((item) => (
            <tr
              key={`${item.namespace}/${item.name}`}
              class="hover:bg-hover/30 cursor-pointer"
              onClick={() => {
                globalThis.location.href = getDetailHref("gateways", item);
              }}
            >
              <td class={TD}>
                <a
                  href={getDetailHref("gateways", item)}
                  class="font-medium text-brand hover:underline"
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.name}
                </a>
              </td>
              <td class={TD_SEC}>{item.namespace}</td>
              <td class={TD_SEC}>{item.gatewayClassName}</td>
              <td class={TD_SEC}>
                {item.listeners?.length ?? 0}
              </td>
              <td class={`${TD_SEC} max-w-[200px] truncate text-xs`}>
                {(item.addresses ?? []).join(", ") || "\u2014"}
              </td>
              <td class={TD}>
                <StatusBadge conditions={item.conditions} />
              </td>
              <td class={TD_SEC}>{formatAge(item.age)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function renderHTTPRoutesTable(items: HTTPRouteSummary[]) {
  if (items.length === 0) {
    return <EmptyTable message="No HTTP routes found." />;
  }
  return (
    <div class="overflow-x-auto rounded-lg border border-border-primary">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border-primary bg-surface">
            <th class={TH}>Name</th>
            <th class={TH}>Namespace</th>
            <th class={TH}>Hostnames</th>
            <th class={TH}>Parents</th>
            <th class={TH}>Backends</th>
            <th class={TH}>Status</th>
            <th class={TH}>Age</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border-subtle">
          {items.map((item) => (
            <tr
              key={`${item.namespace}/${item.name}`}
              class="hover:bg-hover/30 cursor-pointer"
              onClick={() => {
                globalThis.location.href = getDetailHref("httproutes", item);
              }}
            >
              <td class={TD}>
                <a
                  href={getDetailHref("httproutes", item)}
                  class="font-medium text-brand hover:underline"
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.name}
                </a>
              </td>
              <td class={TD_SEC}>{item.namespace}</td>
              <td class={`${TD_SEC} max-w-[200px] truncate text-xs`}>
                {(item.hostnames ?? []).join(", ") || "\u2014"}
              </td>
              <td class={TD_SEC}>
                {item.parentRefs?.length ?? 0}
              </td>
              <td class={TD_SEC}>
                {item.backendCount ?? 0}
              </td>
              <td class={TD}>
                <StatusBadge conditions={item.conditions} />
              </td>
              <td class={TD_SEC}>{formatAge(item.age)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function renderRoutesTable(
  kind: GatewayResourceKind,
  items: RouteSummary[],
) {
  if (items.length === 0) {
    return (
      <EmptyTable
        message={`No ${KIND_LABELS[kind]?.toLowerCase() ?? "routes"} found.`}
      />
    );
  }
  return (
    <div class="overflow-x-auto rounded-lg border border-border-primary">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border-primary bg-surface">
            <th class={TH}>Name</th>
            <th class={TH}>Namespace</th>
            <th class={TH}>Parents</th>
            <th class={TH}>Status</th>
            <th class={TH}>Age</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border-subtle">
          {items.map((item) => (
            <tr
              key={`${item.namespace}/${item.name}`}
              class="hover:bg-hover/30 cursor-pointer"
              onClick={() => {
                globalThis.location.href = getDetailHref(kind, item);
              }}
            >
              <td class={TD}>
                <a
                  href={getDetailHref(kind, item)}
                  class="font-medium text-brand hover:underline"
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.name}
                </a>
              </td>
              <td class={TD_SEC}>{item.namespace}</td>
              <td class={TD_SEC}>
                {item.parentRefs?.length ?? 0}
              </td>
              <td class={TD}>
                <StatusBadge conditions={item.conditions} />
              </td>
              <td class={TD_SEC}>{formatAge(item.age)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
