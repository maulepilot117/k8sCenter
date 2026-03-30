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
  configmaps: number;
  secrets: number;
  serviceaccounts: number;
  resourcequotas: number;
  limitranges: number;
}

const EMPTY_SUMMARY: SummaryData = {
  configmaps: 0,
  secrets: 0,
  serviceaccounts: 0,
  resourcequotas: 0,
  limitranges: 0,
};

const configSection = DOMAIN_SECTIONS.find((s) => s.id === "config")!;

function resolveTab(currentPath: string): {
  kind: string;
  title: string;
  createHref?: string;
} {
  const path = currentPath.replace(/\/$/, "");

  if (path === "/config/configmaps") {
    return {
      kind: "configmaps",
      title: "ConfigMaps",
      createHref: "/config/configmaps/new",
    };
  }

  if (path === "/config/secrets") {
    return {
      kind: "secrets",
      title: "Secrets",
      createHref: "/config/secrets/new",
    };
  }

  if (path === "/config/serviceaccounts") {
    return {
      kind: "serviceaccounts",
      title: "Service Accounts",
    };
  }

  if (path === "/config/resourcequotas") {
    return {
      kind: "resourcequotas",
      title: "Resource Quotas",
    };
  }

  if (path === "/config/limitranges") {
    return {
      kind: "limitranges",
      title: "Limit Ranges",
    };
  }

  // Default (landing /config): show ConfigMaps
  return {
    kind: "configmaps",
    title: "ConfigMaps",
    createHref: "/config/configmaps/new",
  };
}

export default function ConfigDashboard(
  { currentPath }: { currentPath: string },
) {
  const summary = useSignal<SummaryData>(EMPTY_SUMMARY);
  const loading = useSignal(true);
  const namespace = selectedNamespace.value;

  useEffect(() => {
    if (!IS_BROWSER) return;

    loading.value = true;

    const fetchSummary = async () => {
      try {
        const nsParam = namespace && namespace !== "all"
          ? `?namespace=${encodeURIComponent(namespace)}`
          : "";

        const countsRes = await apiGet<Record<string, number>>(
          `/v1/resources/counts${nsParam}`,
        );

        const countsData = countsRes.data ?? {};

        summary.value = {
          configmaps: countsData["configmaps"] ?? 0,
          secrets: countsData["secrets"] ?? 0,
          serviceaccounts: countsData["serviceaccounts"] ?? 0,
          resourcequotas: countsData["resourcequotas"] ?? 0,
          limitranges: countsData["limitranges"] ?? 0,
        };
      } catch {
        summary.value = EMPTY_SUMMARY;
      } finally {
        loading.value = false;
      }
    };

    fetchSummary();
  }, [namespace]);

  const { kind, title, createHref } = resolveTab(currentPath);
  const s = summary.value;

  const summaryCards = [
    {
      label: "ConfigMaps",
      value: s.configmaps,
      displayValue: String(s.configmaps),
      max: Math.max(s.configmaps, 1),
      ringValue: s.configmaps,
      color: "var(--accent)",
    },
    {
      label: "Secrets",
      value: s.secrets,
      displayValue: String(s.secrets),
      max: Math.max(s.secrets, 1),
      ringValue: s.secrets,
      color: "var(--accent-secondary)",
    },
    {
      label: "Service Accounts",
      value: s.serviceaccounts,
      displayValue: String(s.serviceaccounts),
      max: Math.max(s.serviceaccounts, 1),
      ringValue: s.serviceaccounts,
      color: "var(--success)",
    },
    {
      label: "Resource Quotas",
      value: s.resourcequotas,
      displayValue: String(s.resourcequotas),
      max: Math.max(s.resourcequotas, 1),
      ringValue: s.resourcequotas,
      color: "var(--warning)",
    },
    {
      label: "Limit Ranges",
      value: s.limitranges,
      displayValue: String(s.limitranges),
      max: Math.max(s.limitranges, 1),
      ringValue: s.limitranges,
      color: "var(--warning)",
    },
  ];

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Page header */}
      <div class="flex items-center justify-between mb-5">
        <div>
          <h1 class="text-xl font-semibold tracking-tight text-text-primary">
            Configuration
          </h1>
          <p class="text-xs text-text-muted mt-0.5">
            Manage ConfigMaps, Secrets, Service Accounts, and resource
            constraints
          </p>
        </div>
        <div class="flex gap-2">
          <a
            href="/config/secrets/new"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "7px 14px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-secondary)",
              background: "transparent",
              borderRadius: "6px",
              textDecoration: "none",
              border: "1px solid var(--border-primary)",
              cursor: "pointer",
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
              <path d="M4 8h8M8 4v8" />
            </svg>
            New Secret
          </a>
          <a
            href="/config/configmaps/new"
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
            New ConfigMap
          </a>
        </div>
      </div>

      {/* Sub-navigation */}
      <SubNav tabs={configSection.tabs ?? []} currentPath={currentPath} />

      {/* Summary strip */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
          gap: "var(--grid-gap, 12px)",
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

      {/* Content area */}
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
