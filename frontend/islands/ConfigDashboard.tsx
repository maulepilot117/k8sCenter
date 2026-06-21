import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import SubNav from "@/islands/SubNav.tsx";
import ResourceTable from "@/islands/ResourceTable.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";

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
    { label: "ConfigMaps", value: s.configmaps, color: "var(--accent)" },
    { label: "Secrets", value: s.secrets, color: "var(--accent-2)" },
    {
      label: "Service Accounts",
      value: s.serviceaccounts,
      color: "var(--success)",
    },
    {
      label: "Resource Quotas",
      value: s.resourcequotas,
      color: "var(--warning)",
    },
    { label: "Limit Ranges", value: s.limitranges, color: "var(--warning)" },
  ];

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Page header */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          marginBottom: "20px",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              lineHeight: 1.2,
            }}
          >
            Configuration
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Manage ConfigMaps, Secrets, Service Accounts, and resource
            constraints
          </p>
        </div>
        <div style={{ display: "flex", gap: "8px", alignItems: "center" }}>
          <a
            href="/config/secrets/new"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "8px 14px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
              background: "transparent",
              borderRadius: "9px",
              textDecoration: "none",
              border: "1px solid var(--border-primary)",
              cursor: "pointer",
            }}
          >
            <svg
              width="14"
              height="14"
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
              padding: "8px 16px",
              fontSize: "13px",
              fontWeight: 600,
              color: "var(--bg-base)",
              background: "var(--accent)",
              borderRadius: "9px",
              textDecoration: "none",
              border: "none",
              cursor: "pointer",
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
            New ConfigMap
          </a>
        </div>
      </div>

      {/* Sub-navigation */}
      <SubNav tabs={flattenGroups(configSection)} currentPath={currentPath} />

      {/* Summary strip \u2014 WidgetShell KPI tiles (glass chrome, data inside) */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "12px",
          marginBottom: "20px",
        }}
      >
        {summaryCards.map((card) => (
          <WidgetShell
            key={card.label}
            padding={16}
            style={{ flex: "1 1 140px", minWidth: "130px" }}
          >
            <div
              style={{
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.05em",
                color: "var(--text-muted)",
                marginBottom: "6px",
              }}
            >
              {card.label}
            </div>
            <div
              style={{
                fontSize: "24px",
                fontWeight: 700,
                fontFamily: "var(--font-mono)",
                color: loading.value ? "var(--text-muted)" : card.color,
                lineHeight: 1,
                fontVariantNumeric: "tabular-nums",
              }}
            >
              {loading.value ? "\u2014" : String(card.value)}
            </div>
          </WidgetShell>
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
