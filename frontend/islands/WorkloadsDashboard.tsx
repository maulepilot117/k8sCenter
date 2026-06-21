import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import SubNav from "@/islands/SubNav.tsx";
import ResourceTable from "@/islands/ResourceTable.tsx";
import { SummaryRing } from "@/components/ui/SummaryRing.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";

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
  const tabs = flattenGroups(workloadsSection);
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
          apiGet<
            Array<{
              status?: Record<string, unknown>;
            }>
          >(`/v1/resources/deployments${nsPath}?limit=500`),
          apiGet<
            Array<{
              status?: {
                phase?: string;
                conditions?: Array<{ type: string; status: string }>;
              };
            }>
          >(`/v1/resources/pods${nsPath}?limit=500`),
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
  ];

  return (
    <div class="flex flex-col h-full">
      {/* Page header \u2014 24/700 per archetype spec */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: "16px",
          marginBottom: "20px",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              color: "var(--text-primary)",
              lineHeight: 1.2,
            }}
          >
            {title}
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Manage deployments, pods, jobs, and other workload resources
          </p>
        </div>
        <a
          href={createHref ?? "/workloads/deployments/new"}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: "6px",
            padding: "8px 16px",
            fontSize: "13px",
            fontWeight: 600,
            color: "var(--bg-base)",
            background: "var(--accent)",
            borderRadius: "9px",
            textDecoration: "none",
            border: "none",
            cursor: "pointer",
            whiteSpace: "nowrap",
            flexShrink: 0,
          }}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            stroke-width="2.5"
          >
            <path d="M4 8h8M8 4v8" />
          </svg>
          New {title.replace(/s$/, "")}
        </a>
      </div>

      {/* Sub-navigation */}
      <SubNav
        tabs={flattenGroups(workloadsSection)}
        currentPath={currentPath}
      />

      {/* Summary strip \u2014 WidgetShell (glass) cards per dashboard archetype */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))",
          gap: "12px",
          marginBottom: "20px",
        }}
      >
        {summaryCards.map((card) => (
          <WidgetShell key={card.label} padding={16}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "12px",
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
                    fontSize: "11px",
                    fontWeight: 600,
                    letterSpacing: "0.05em",
                    textTransform: "uppercase",
                    color: "var(--text-muted)",
                    marginBottom: "2px",
                  }}
                >
                  {card.label}
                </div>
                <div
                  style={{
                    fontSize: "20px",
                    fontWeight: 700,
                    fontFamily: "var(--font-mono)",
                    color: card.color,
                    lineHeight: 1.1,
                  }}
                >
                  {loading.value ? "\u2014" : card.displayValue}
                </div>
              </div>
            </div>
          </WidgetShell>
        ))}
      </div>

      {/* Resource table \u2014 solid surface, no backdrop-filter */}
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
