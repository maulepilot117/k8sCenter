import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import LogFilterBar from "@/islands/LogFilterBar.tsx";
import LogResults from "@/islands/LogResults.tsx";
import LogLiveTail from "@/islands/LogLiveTail.tsx";
import LogVolumeHistogram from "@/islands/LogVolumeHistogram.tsx";
import type { LogLine, VolumeEntry } from "@/lib/types/logs.ts";

interface LokiStatus {
  detected: boolean;
  url?: string;
  detectedVia?: string;
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
  const lokiAvailable = useSignal<boolean | null>(null);
  const searchResults = useSignal<LogLine[]>([]);
  const volumeData = useSignal<VolumeEntry[] | null>(null);
  const loading = useSignal(false);
  const error = useSignal<string | null>(null);
  const isTailing = useSignal(false);
  const tailQuery = useSignal("");
  const tailNamespace = useSignal("");
  const tailLines = useSignal<LogLine[]>([]);

  // Check Loki availability on mount
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<LokiStatus>("/v1/logs/status")
      .then((res) => {
        lokiAvailable.value = res.data?.detected ?? false;
      })
      .catch(() => {
        lokiAvailable.value = false;
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
        error.value = err.message ?? "Query failed";
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

  if (lokiAvailable.value === null) {
    return (
      <div class="flex items-center justify-center p-12 text-sm text-text-muted">
        Checking Loki availability...
      </div>
    );
  }

  if (!lokiAvailable.value) {
    return (
      <div class="rounded-lg border border-border-primary bg-bg-surface p-8 text-center">
        <h3 class="text-lg font-semibold text-text-primary">
          Loki Not Detected
        </h3>
        <p class="mt-2 text-sm text-text-secondary">
          Log aggregation requires Loki. Deploy Loki to your cluster or set{" "}
          <code class="text-accent-primary">KUBECENTER_LOKI_URL</code>{" "}
          to connect to an external instance.
        </p>
      </div>
    );
  }

  return (
    <div class="space-y-3">
      <LogFilterBar
        onSearch={handleSearch}
        onLiveTail={handleLiveTail}
        onStopTail={handleStopTail}
        isTailing={isTailing}
        loading={loading}
      />
      <LogVolumeHistogram data={volumeData} />
      {error.value && <ErrorBanner message={error.value} />}
      {isTailing.value
        ? (
          <LogLiveTail
            active={isTailing}
            query={tailQuery}
            namespace={tailNamespace}
            lines={tailLines}
          />
        )
        : <LogResults lines={searchResults} loading={loading} />}
    </div>
  );
}
