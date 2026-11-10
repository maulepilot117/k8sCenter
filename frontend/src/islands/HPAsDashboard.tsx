/** @jsxImportSource react */
import { useEffect, useState } from "react";
import HPAWizard from "@/src/islands/HPAWizard.tsx";
import ResourceTable from "@/src/islands/ResourceTable.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { formatCount, resourceCounts } from "@/src/lib/resource-counts.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

export default function HPAsDashboard() {
  const [wizardOpen, setWizardOpen] = useState(false);

  // Auto-open wizard when navigated with ?action=create (e.g. from CommandPalette)
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (
      new URL(globalThis.location.href).searchParams.get("action") === "create"
    ) {
      setWizardOpen(true);
      const url = new URL(globalThis.location.href);
      url.searchParams.delete("action");
      globalThis.history.replaceState({}, "", url.toString());
    }
  }, []);

  const countsReady = useSignalValue(resourceCounts) !== null;
  const subtitle = countsReady
    ? `${formatCount("hpas") ?? 0} horizontal pod autoscalers`
    : "Loading horizontal pod autoscalers…";

  return (
    <div className="flex flex-col h-full">
      {/* Floating wizard modal */}
      {wizardOpen && <HPAWizard onClose={() => setWizardOpen(false)} />}

      {/* Page header */}
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
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              lineHeight: 1.2,
            }}
          >
            HorizontalPodAutoscalers
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            {subtitle}
          </p>
        </div>
        <button
          type="button"
          onClick={() => setWizardOpen(true)}
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
            border: "none",
            cursor: "pointer",
            whiteSpace: "nowrap",
            flexShrink: 0,
            fontFamily: "inherit",
          }}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
          >
            <path d="M4 8h8M8 4v8" />
          </svg>
          New HPA
        </button>
      </div>

      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ResourceTable
          kind="hpas"
          title="HorizontalPodAutoscalers"
          createHref="/scaling/hpas/new"
          hideHeader
        />
      </div>
    </div>
  );
}
