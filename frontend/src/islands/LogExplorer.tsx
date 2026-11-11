import { useEffect, useState } from "react";
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
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

/** The selected cluster's Loki, as GET /logs/status reports it. */
interface LokiStatus {
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
 * The client's own reason for a status request that failed without one (a
 * network error, a 5xx, a refusal with no reason); never sent by the
 * server. It must not read as "no Loki", which would send the operator to
 * deploy one.
 */
const STATUS_UNREADABLE = "status_unreadable";

/**
 * The copy for each reason a cluster's Loki cannot be used: Loki's own two
 * (loki/resolver.go), the shared remote ones, and the client's
 * STATUS_UNREADABLE. Keyed on the reason, never the server's message.
 * `local` is whether the selected cluster is the local one, whose Loki is
 * set by deployment config rather than a binding. A query refused for one
 * of these reasons uses the same copy, so a reason reads the same wherever
 * it surfaces, and this table is the one list of them.
 */
const UNAVAILABLE_COPY: Record<
  string,
  (label: string, local: boolean) => UnavailableCopy
> = {
  // Also the copy for a local cluster whose discovery found nothing, which
  // answers with no reason.
  loki_not_found: (label, local) =>
    local
      ? {
          title: "Loki Not Detected",
          body: "Log aggregation requires Loki. Deploy Loki to your cluster or set KUBECENTER_LOKI_URL to connect to an external instance.",
        }
      : {
          title: "Loki Not Found",
          body: `No Loki Service was found on ${label} in the ${LOKI_NAMESPACES} namespace. Deploy Loki there, or configure a Loki URL for this cluster in Settings > Clusters.`,
        },
  loki_unreachable: (label, local) => ({
    title: "Loki Unreachable",
    body: `Loki on ${label} was found but did not answer. Check that it is running${local ? "" : ", and the cluster's configured Loki URL if it has one"}.`,
  }),
  forbidden: (label) => ({
    title: "Permission Needed",
    body: `Your Kubernetes permissions on ${label} do not let k8sCenter reach its Loki. Log search needs get on services in the ${LOKI_NAMESPACES} namespace and list on services cluster-wide, to find Loki, and create on pods/portforward and list on pods in Loki's namespace.`,
  }),
  credentials_invalid: (label) => ({
    title: "Credentials Refused",
    body: `k8sCenter could not connect to Loki on ${label} with the credentials it has. Check the cluster's stored credentials, and the token of its configured Loki URL in Settings > Clusters.`,
  }),
  unreachable: (label) => ({
    title: "Cluster Unreachable",
    body: `${label} could not be reached, so its Loki cannot be searched.`,
  }),
  cluster_unknown: (label) => ({
    title: "Cluster Not Registered",
    body: `${label} is no longer registered with k8sCenter. Select another cluster.`,
  }),
  db_unavailable: (label) => ({
    title: "Cluster Registry Unavailable",
    body: `The cluster registry could not be read, so the Loki of ${label} cannot be found. Try again shortly.`,
  }),
  unsupported_platform: (label) => ({
    title: "Log Search Unavailable",
    body: `Log search is not available for ${label} yet.`,
  }),
  [STATUS_UNREADABLE]: (label) => ({
    title: "Loki Status Unavailable",
    body: `k8sCenter could not read the Loki status for ${label}. Reload the page to try again.`,
  }),
};

/** The copy for a reason, or for "not found" when it is not a known one. */
function lokiUnavailableCopy(
  reason: string | undefined,
  label: string,
  local: boolean,
): UnavailableCopy {
  const copy =
    (reason && Object.hasOwn(UNAVAILABLE_COPY, reason)
      ? UNAVAILABLE_COPY[reason]
      : undefined) ?? UNAVAILABLE_COPY.loki_not_found;
  return copy(label, local);
}

/** Where the selected cluster's Loki is read from, for the header. */
function lokiSourceLabel(status: LokiStatus): string {
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

/** The copy for a failed query: its reason's copy when it has one. */
function queryErrorMessage(
  err: unknown,
  label: string,
  local: boolean,
): string {
  if (
    err instanceof ApiError &&
    err.reason &&
    Object.hasOwn(UNAVAILABLE_COPY, err.reason)
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
  const isClient = useIsClient();
  const clusterId = useSignalValue(selectedCluster);
  const local = clusterId === LOCAL_CLUSTER_ID;
  const names = useClusterNames([clusterId]);
  const label = clusterLabel(clusterId, names);
  const [lokiStatus, setLokiStatus] = useState<LokiStatus | null>(null);
  const [searchResults, setSearchResults] = useState<LogLine[]>([]);
  const [volumeData, setVolumeData] = useState<VolumeEntry[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isTailing, setIsTailing] = useState(false);
  const [tailQuery, setTailQuery] = useState("");
  const [tailNamespace, setTailNamespace] = useState("");
  const [tailLines, setTailLines] = useState<LogLine[]>([]);

  // A failed query is worded when it fails, with the label and cluster as
  // they are then, not as they were when it was sent.
  const labelRef = useLatest(label);
  const localRef = useLatest(local);

  // Check the selected cluster's Loki on mount. A status that cannot be
  // read is treated as Loki being unavailable, for the reason it gave, or
  // as unreadable when it gave none.
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<LokiStatus>("/v1/logs/status")
      .then((res) => {
        setLokiStatus(res.data ?? { detected: false });
      })
      .catch((err) => {
        setLokiStatus({
          detected: false,
          reason:
            (err instanceof ApiError ? err.reason : undefined) ??
            STATUS_UNREADABLE,
        });
      });
  }, []);

  function handleSearch(
    query: string,
    start: string,
    end: string,
    limit: number,
    direction: string,
  ) {
    setLoading(true);
    setError(null);

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
        setSearchResults(lines);
      })
      .catch((err) => {
        setError(queryErrorMessage(err, labelRef.current, localRef.current));
      })
      .finally(() => {
        setLoading(false);
      });

    // Also fetch volume data
    const volParams = new URLSearchParams({ query, start, end, step: "1m" });
    apiGet<VolumeResponseData>(`/v1/logs/volume?${volParams}`)
      .then((res) => {
        setVolumeData(res.data?.data?.result ?? null);
      })
      .catch(() => {}); // non-critical
  }

  function handleLiveTail(query: string, namespace: string) {
    setTailQuery(query);
    setTailNamespace(namespace);
    setTailLines([]);
    setIsTailing(true);
  }

  function handleStopTail() {
    setIsTailing(false);
  }

  if (!isClient) return null;

  const status = lokiStatus;
  if (status === null) {
    return (
      <div className="flex items-center justify-center p-12 text-sm text-text-muted">
        Checking Loki availability...
      </div>
    );
  }

  if (!status.detected) {
    const copy = lokiUnavailableCopy(status.reason, label, local);
    return (
      <div
        className="rounded-lg border border-border-primary bg-surface p-8 text-center"
        data-testid="loki-unavailable"
      >
        <h3 className="text-lg font-semibold text-text-primary">
          {copy.title}
        </h3>
        <p className="mt-2 text-sm text-text-secondary">{copy.body}</p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-xs text-text-muted" data-testid="loki-source">
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
      {error && <ErrorBanner message={error} />}
      {isTailing ? (
        <LogLiveTail
          active={isTailing}
          query={tailQuery}
          namespace={tailNamespace}
          lines={tailLines}
          setLines={setTailLines}
        />
      ) : (
        <LogResults lines={searchResults} loading={loading} />
      )}
    </div>
  );
}
