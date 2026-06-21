import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";

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
    <div
      style={{
        display: "flex",
        flexWrap: "wrap",
        gap: "16px",
      }}
    >
      {dashboards.value.map((d) => (
        <a
          key={d.uid}
          href={`/api/v1/monitoring/grafana/proxy${d.url}?kiosk=1`}
          target="_blank"
          rel="noopener noreferrer"
          style={{
            flex: "1 1 240px",
            minWidth: "200px",
            textDecoration: "none",
            display: "block",
          }}
        >
          <GlassCard
            padding={16}
            style={{ height: "100%", transition: "border-color 0.15s" }}
          >
            <h3
              style={{
                margin: 0,
                fontSize: "14px",
                fontWeight: 650,
                color: "var(--text-primary)",
              }}
            >
              {d.title}
            </h3>
            {d.tags && d.tags.length > 0 && (
              <div
                style={{
                  marginTop: "10px",
                  display: "flex",
                  flexWrap: "wrap",
                  gap: "4px",
                }}
              >
                {d.tags.map((tag) => (
                  <span
                    key={tag}
                    style={{
                      borderRadius: "6px",
                      background: "var(--bg-elevated)",
                      padding: "2px 8px",
                      fontSize: "11px",
                      color: "var(--text-muted)",
                    }}
                  >
                    {tag}
                  </span>
                ))}
              </div>
            )}
            <p
              style={{
                marginTop: "10px",
                marginBottom: 0,
                fontSize: "11px",
                color: "var(--text-muted)",
                fontFamily: "monospace",
              }}
            >
              {d.uid}
            </p>
          </GlassCard>
        </a>
      ))}
    </div>
  );
}
