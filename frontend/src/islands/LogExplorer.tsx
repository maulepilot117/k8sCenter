import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import type { LogLine, VolumeEntry } from "@/lib/types/logs.ts";
import LogFilterBar from "@/src/islands/LogFilterBar.tsx";
import LogLiveTail from "@/src/islands/LogLiveTail.tsx";
import LogResults from "@/src/islands/LogResults.tsx";
import LogVolumeHistogram from "@/src/islands/LogVolumeHistogram.tsx";
import { LOCAL_CLUSTER_ID, selectedCluster } from "@/src/lib/cluster.ts";
import { clusterLabel, useClusterNames } from "@/src/lib/cluster-label.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/** The selected cluster's Loki, as GET /logs/status reports it. */
export interface LokiStatus {
  detected: boolean;
  url?: string;
  /**
   * How Loki was found: a local discovery method (config-override,
   * service-name, service-label), or for a remote cluster binding or
   * port-forward.
   */
  detectedVia?: string;
  /** The Service the port-forward path reaches Loki through. */
  namespace?: string;
  service?: string;
  /** Why Loki is unavailable, when it is not detected. */
  reason?: string;
}

interface UnavailableCopy {
  title: string;
  body: string;
}

const LOKI_NAMESPACES = "monitoring, loki or observability";

/**
 * The copy for a cluster whose Loki cannot be used, keyed on the status
 * reason, never the server's message. `local` is whether the selected
 * cluster is the local one, whose Loki is set by deployment config rather
 * than a binding. A query refused for one of these reasons uses the same
 * copy, so a reason reads the same wherever it surfaces.
 */
export function lokiUnavailableCopy(
  reason: string | undefined,
  label: string,
  local: boolean,
): UnavailableCopy {
  switch (reason) {
    case "loki_unreachable":
      return {
        title: "Loki Unreachable",
        body: `Loki on ${label} was found but did not answer. Check that it is running${local ? "" : ", and the cluster's configured Loki URL if it has one"}.`,
      };
    case "forbidden":
      return {
        title: "Permission Needed",
        body: `Your Kubernetes permissions on ${label} do not let k8sCenter reach its Loki. Log search needs get on services in the ${LOKI_NAMESPACES} namespace, and create on pods/portforward and list on pods in Loki's namespace.`,
      };
    case "credentials_invalid":
      return {
        title: "Credentials Refused",
        body: `k8sCenter could not connect to Loki on ${label} with the credentials it has. Check the cluster's stored credentials, and the token of its configured Loki URL in Settings > Clusters.`,
      };
    case "unreachable":
      return {
        title: "Cluster Unreachable",
        body: `${label} could not be reached, so its Loki cannot be searched.`,
      };
    case "cluster_unknown":
      return {
        title: "Cluster Not Registered",
        body: `${label} is no longer registered with k8sCenter. Select another cluster.`,
      };
    case "db_unavailable":
      return {
        title: "Cluster Registry Unavailable",
        body: "The cluster registry could not be read. Try again shortly.",
      };
    case "unsupported_platform":
      return {
        title: "Log Search Unavailable",
        body: `Log search is not available for ${label} yet.`,
      };
  }
  // loki_not_found, and a local cluster whose discovery found nothing.
  return local
    ? {
        title: "Loki Not Detected",
        body: "Log aggregation requires Loki. Deploy Loki to your cluster or set KUBECENTER_LOKI_URL to connect to an external instance.",
      }
    : {
        title: "Loki Not Found",
        body: `No Loki Service was found on ${label} in the ${LOKI_NAMESPACES} namespace. Deploy Loki there, or configure a Loki URL for this cluster in Settings > Clusters.`,
      };
}

/** Where the selected cluster's Loki is read from, for the header. */
export function lokiSourceLabel(status: LokiStatus): string {
  switch (status.detectedVia) {
    case "binding":
    case "config-override":
      return "configured URL";
    case "port-forward":
      return status.namespace && status.service
        ? `Service ${status.namespace}/${status.service}`
        : "in-cluster Service";
  }
  return "discovered in-cluster Service";
}

// The reasons a cluster's Loki is unavailable (loki/resolver.go): Loki's
// own two and the shared remote ones.
const UNAVAILABLE_REASONS = new Set([
  "loki_not_found",
  "loki_unreachable",
  "forbidden",
  "credentials_invalid",
  "unreachable",
  "cluster_unknown",
  "db_unavailable",
  "unsupported_platform",
]);

/** The copy for a failed query: its reason's copy when it has one. */
function queryErrorMessage(
  err: unknown,
  label: string,
  local: boolean,
): string {
  if (
    err instanceof ApiError &&
    err.reason &&
    UNAVAILABLE_REASONS.has(err.reason)
  ) {
    return lokiUnavailableCopy(err.reason, label, local).body;
  }
  return err instanceof Error && err.message ? err.message : "Query failed";
}

interface QueryResponseData {
  data: {
    result: Array<{
      stream: Record<string, string>;
      values: string[][];
    }>;
  };
}

interface VolumeResponseData {
  data: {
    result: VolumeEntry[];
  };
}

export default function LogExplorer() {
  // A cluster switch reloads the page (ClusterSwitcher), so the cluster read
  // here is the one every request below goes to.
  const clusterId = selectedCluster.value;
  const local = clusterId === LOCAL_CLUSTER_ID;
  const names = useClusterNames([clusterId]);
  const label = clusterLabel(clusterId, names.value);
  const lokiStatus = useSignal<LokiStatus | null>(null);
  const searchResults = useSignal<LogLine[]>([]);
  const volumeData = useSignal<VolumeEntry[] | null>(null);
  const loading = useSignal(false);
  const error = useSignal<string | null>(null);
  const isTailing = useSignal(false);
  const tailQuery = useSignal("");
  const tailNamespace = useSignal("");
  const tailLines = useSignal<LogLine[]>([]);

  // Check the selected cluster's Loki on mount. A status that cannot be
  // read is treated as Loki being unavailable, for the reason it gave.
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<LokiStatus>("/v1/logs/status")
      .then((res) => {
        lokiStatus.value = res.data ?? { detected: false };
      })
      .catch((err) => {
        lokiStatus.value = {
          detected: false,
          reason: err instanceof ApiError ? err.reason : undefined,
        };
      });
  }, []);

  function handleSearch(
    query: string,
    start: string,
    end: string,
    limit: number,
    direction: string,
  ) {
    loading.value = true;
    error.value = null;

    const params = new URLSearchParams({
      query,
      start,
      end,
      limit: String(limit),
      direction,
    });
    apiGet<QueryResponseData>(`/v1/logs/query?${params}`)
      .then((res) => {
        const lines: LogLine[] = [];
        for (const stream of res.data?.data?.result ?? []) {
          for (const [ts, line] of stream.values ?? []) {
            lines.push({ timestamp: ts, line, labels: stream.stream ?? {} });
          }
        }
        searchResults.value = lines;
      })
      .catch((err) => {
        error.value = queryErrorMessage(err, label, local);
      })
      .finally(() => {
        loading.value = false;
      });

    // Also fetch volume data
    const volParams = new URLSearchParams({ query, start, end, step: "1m" });
    apiGet<VolumeResponseData>(`/v1/logs/volume?${volParams}`)
      .then((res) => {
        volumeData.value = res.data?.data?.result ?? null;
      })
      .catch(() => {}); // non-critical
  }

  function handleLiveTail(query: string, namespace: string) {
    tailQuery.value = query;
    tailNamespace.value = namespace;
    tailLines.value = [];
    isTailing.value = true;
  }

  function handleStopTail() {
    isTailing.value = false;
  }

  if (!IS_BROWSER) return null;

  const status = lokiStatus.value;
  if (status === null) {
    return (
      <div class="flex items-center justify-center p-12 text-sm text-text-muted">
        Checking Loki availability...
      </div>
    );
  }

  if (!status.detected) {
    const copy = lokiUnavailableCopy(status.reason, label, local);
    return (
      <div
        class="rounded-lg border border-border-primary bg-bg-surface p-8 text-center"
        data-testid="loki-unavailable"
      >
        <h3 class="text-lg font-semibold text-text-primary">{copy.title}</h3>
        <p class="mt-2 text-sm text-text-secondary">{copy.body}</p>
      </div>
    );
  }

  return (
    <div class="space-y-3">
      <p class="text-xs text-text-muted" data-testid="loki-source">
        Searching {label} &middot; Loki: {lokiSourceLabel(status)}
      </p>
      <LogFilterBar
        onSearch={handleSearch}
        onLiveTail={handleLiveTail}
        onStopTail={handleStopTail}
        isTailing={isTailing}
        loading={loading}
      />
      <LogVolumeHistogram data={volumeData} />
      {error.value && <ErrorBanner message={error.value} />}
      {isTailing.value ? (
        <LogLiveTail
          active={isTailing}
          query={tailQuery}
          namespace={tailNamespace}
          lines={tailLines}
        />
      ) : (
        <LogResults lines={searchResults} loading={loading} />
      )}
    </div>
  );
}
