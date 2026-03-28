import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

interface GrafanaDashboard {
  uid: string;
  title: string;
  url: string;
  tags: string[];
  type: string;
}

export default function MonitoringDashboards() {
  const dashboards = useSignal<GrafanaDashboard[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;

    apiGet<GrafanaDashboard[]>("/v1/monitoring/dashboards")
      .then((res) => {
        dashboards.value = res.data;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to load dashboards";
      })
      .finally(() => {
        loading.value = false;
      });
  }, []);

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex items-center justify-center p-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (error.value) {
    return <ErrorBanner message={error.value} />;
  }

  if (dashboards.value.length === 0) {
    return (
      <div class="py-12 text-center text-sm text-text-muted">
        <p class="text-lg font-medium text-text-muted">
          No Dashboards Found
        </p>
        <p class="mt-1">
          Dashboards are provisioned when Grafana is detected and configured.
        </p>
        <a
          href="/monitoring"
          class="mt-3 inline-block text-brand hover:underline"
        >
          Check monitoring status
        </a>
      </div>
    );
  }

  return (
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {dashboards.value.map((d) => (
        <a
          key={d.uid}
          href={`/api/v1/monitoring/grafana/proxy${d.url}?kiosk=1`}
          target="_blank"
          rel="noopener noreferrer"
          class="group rounded-lg border border-border-primary bg-surface p-4 transition-colors hover:border-brand"
        >
          <h3 class="font-medium text-text-primary group-hover:text-brand text-text-primary">
            {d.title}
          </h3>
          {d.tags && d.tags.length > 0 && (
            <div class="mt-2 flex flex-wrap gap-1">
              {d.tags.map((tag) => (
                <span
                  key={tag}
                  class="rounded-full bg-elevated px-2 py-0.5 text-xs text-text-secondary bg-elevated text-text-muted"
                >
                  {tag}
                </span>
              ))}
            </div>
          )}
          <p class="mt-2 text-xs text-text-muted">
            UID: {d.uid}
          </p>
        </a>
      ))}
    </div>
  );
}
