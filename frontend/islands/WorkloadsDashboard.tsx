import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import SubNav from "@/islands/SubNav.tsx";
import ResourceTable from "@/islands/ResourceTable.tsx";
import { SummaryRing } from "@/components/ui/SummaryRing.tsx";

interface SummaryData {
  totalDeployments: number;
  availableDeployments: number;
  progressingDeployments: number;
  failedDeployments: number;
  totalPods: number;
  readyPods: number;
}

const EMPTY_SUMMARY: SummaryData = {
  totalDeployments: 0,
  availableDeployments: 0,
  progressingDeployments: 0,
  failedDeployments: 0,
  totalPods: 0,
  readyPods: 0,
};

const workloadsSection = DOMAIN_SECTIONS.find((s) => s.id === "workloads")!;

function resolveKind(currentPath: string): {
  kind: string;
  title: string;
  createHref?: string;
} {
  const tabs = workloadsSection.tabs ?? [];
  for (const tab of tabs) {
    if (
      tab.href === currentPath ||
      (currentPath.startsWith(tab.href) &&
        currentPath[tab.href.length] === "/")
    ) {
      const label = tab.label;
      return {
        kind: tab.kind!,
        title: label,
        createHref: `${tab.href}/new`,
      };
    }
  }
  // Default: Deployments
  return {
    kind: "deployments",
    title: "Deployments",
    createHref: "/workloads/deployments/new",
  };
}

export default function WorkloadsDashboard(
  { currentPath }: { currentPath: string },
) {
  const summary = useSignal<SummaryData>(EMPTY_SUMMARY);
  const loading = useSignal(true);
  const namespace = selectedNamespace.value;

  useEffect(() => {
    if (!IS_BROWSER) return;

    loading.value = true;

    const nsPath = namespace && namespace !== "all" ? `/${namespace}` : "";

    const fetchSummary = async () => {
      try {
        const nsParam = namespace && namespace !== "all"
          ? `?namespace=${encodeURIComponent(namespace)}`
          : "";

        const [countsRes, deploymentsRes, podsRes] = await Promise.all([
          apiGet<Record<string, number>>(
            `/v1/resources/counts${nsParam}`,
          ),
          apiGet<{
            data: Array<{
              status?: Record<string, unknown>;
            }>;
          }>(`/v1/resources/deployments${nsPath}?limit=500`),
          apiGet<{
            data: Array<{
              status?: {
                phase?: string;
                conditions?: Array<{ type: string; status: string }>;
              };
            }>;
          }>(`/v1/resources/pods${nsPath}?limit=500`),
        ]);

        const countsData = countsRes.data ?? {};

        const deps = deploymentsRes.data ?? [];
        const totalDeps = countsData["deployments"] ?? deps.length;
        let availableDeps = 0;
        let progressingDeps = 0;
        let failedDeps = 0;

        for (const d of deps) {
          const status = d.status as Record<string, unknown> | undefined;
          const conditions = status?.conditions as
            | Array<{ type: string; status: string }>
            | undefined;

          let isAvailable = false;
          let isProgressing = false;
          let isFailed = false;

          if (conditions) {
            for (const c of conditions) {
              if (c.type === "Available" && c.status === "True") {
                isAvailable = true;
              }
              if (c.type === "Progressing" && c.status === "True") {
                isProgressing = true;
              }
              if (c.type === "Available" && c.status === "False") {
                isFailed = true;
              }
            }
          }

          if (isAvailable) {
            availableDeps++;
          } else if (isFailed) {
            failedDeps++;
          } else if (isProgressing) {
            progressingDeps++;
          }
        }

        const pods = podsRes.data ?? [];
        const totalPods = countsData["pods"] ?? pods.length;
        let readyPods = 0;
        for (const p of pods) {
          const phase = p.status?.phase;
          if (phase === "Running" || phase === "Succeeded") readyPods++;
        }

        summary.value = {
          totalDeployments: totalDeps,
          availableDeployments: availableDeps,
          progressingDeployments: progressingDeps,
          failedDeployments: failedDeps,
          totalPods: totalPods,
          readyPods: readyPods,
        };
      } catch {
        summary.value = EMPTY_SUMMARY;
      } finally {
        loading.value = false;
      }
    };

    fetchSummary();
  }, [namespace]);

  const { kind, title, createHref } = resolveKind(currentPath);
  const s = summary.value;

  const summaryCards = [
    {
      label: "Total",
      value: s.totalDeployments,
      displayValue: String(s.totalDeployments),
      max: Math.max(s.totalDeployments, 1),
      ringValue: s.totalDeployments,
      color: "var(--success)",
    },
    {
      label: "Available",
      value: s.availableDeployments,
      displayValue: String(s.availableDeployments),
      max: Math.max(s.totalDeployments, 1),
      ringValue: s.availableDeployments,
      color: "var(--success)",
    },
    {
      label: "Progressing",
      value: s.progressingDeployments,
      displayValue: String(s.progressingDeployments),
      max: Math.max(s.totalDeployments, 1),
      ringValue: s.progressingDeployments,
      color: "var(--warning)",
    },
    {
      label: "Failed",
      value: s.failedDeployments,
      displayValue: String(s.failedDeployments),
      max: Math.max(s.totalDeployments, 1),
      ringValue: s.failedDeployments,
      color: "var(--error)",
    },
    {
      label: "Pods Ready",
      value: s.readyPods,
      displayValue: `${s.readyPods}/${s.totalPods}`,
      max: Math.max(s.totalPods, 1),
      ringValue: s.readyPods,
      color: "var(--accent)",
    },
    {
      label: "CPU Usage",
      value: 0,
      displayValue: "\u2014",
      max: 100,
      ringValue: 0,
      color: "var(--accent-secondary)",
    },
  ];

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Page header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: "20px",
        }}
      >
        <div>
          <h1
            style={{
              fontSize: "20px",
              fontWeight: 600,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              margin: 0,
            }}
          >
            Workloads
          </h1>
          <p
            style={{
              fontSize: "13px",
              color: "var(--text-muted)",
              marginTop: "2px",
              marginBottom: 0,
            }}
          >
            Manage deployments, pods, jobs, and other workload resources
          </p>
        </div>
        <div style={{ display: "flex", gap: "8px" }}>
          <button
            type="button"
            style={{
              display: "flex",
              alignItems: "center",
              gap: "6px",
              padding: "7px 14px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-secondary)",
              background: "transparent",
              border: "1px solid var(--border-primary)",
              borderRadius: "6px",
              cursor: "pointer",
              fontFamily: "inherit",
            }}
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              <path d="M2 4h12M5 8h6M8 12h0" />
            </svg>
            Filter
          </button>
          <a
            href={createHref ?? "/workloads/deployments/new"}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "7px 14px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--bg-base)",
              background: "var(--accent)",
              borderRadius: "6px",
              textDecoration: "none",
              border: "none",
              cursor: "pointer",
            }}
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
            >
              <path d="M4 8h8M8 4v8" />
            </svg>
            New Workload
          </a>
        </div>
      </div>

      {/* Sub-navigation */}
      <SubNav tabs={workloadsSection.tabs ?? []} currentPath={currentPath} />

      {/* Summary strip */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(6, 1fr)",
          gap: "12px",
          marginBottom: "20px",
        }}
      >
        {summaryCards.map((card) => (
          <div
            key={card.label}
            style={{
              display: "flex",
              alignItems: "center",
              gap: "12px",
              padding: "14px 16px",
              borderRadius: "10px",
              background: "var(--bg-surface)",
              border: "1px solid var(--border-primary)",
              cursor: "pointer",
              transition: "border-color 0.2s ease",
            }}
          >
            <SummaryRing
              value={loading.value ? 0 : card.ringValue}
              max={card.max}
              size={40}
              color={card.color}
            />
            <div style={{ minWidth: 0 }}>
              <div
                style={{
                  fontSize: "12px",
                  color: "var(--text-secondary)",
                }}
              >
                {card.label}
              </div>
              <div
                style={{
                  fontSize: "16px",
                  fontWeight: 600,
                  fontFamily: "var(--font-mono)",
                  color: card.color,
                }}
              >
                {loading.value ? "\u2014" : card.displayValue}
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Resource table */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ResourceTable
          kind={kind}
          title={title}
          createHref={createHref}
          hideHeader
        />
      </div>
    </div>
  );
}
