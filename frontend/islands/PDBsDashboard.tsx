import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import ResourceTable from "@/islands/ResourceTable.tsx";
import PDBWizard from "@/islands/PDBWizard.tsx";
import { selectedNamespace } from "@/lib/namespace.ts";
import { getCount, resourceCounts } from "@/lib/resource-counts.ts";

export default function PDBsDashboard() {
  const _ns = selectedNamespace.value;
  const wizardOpen = useSignal(false);

  // Auto-open wizard when navigated with ?action=create (e.g. from CommandPalette)
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (
      new URL(globalThis.location.href).searchParams.get("action") === "create"
    ) {
      wizardOpen.value = true;
      const url = new URL(globalThis.location.href);
      url.searchParams.delete("action");
      globalThis.history.replaceState({}, "", url.toString());
    }
  }, []);

  const total = getCount("poddisruptionbudgets") ?? 0;
  const countsReady = resourceCounts.value !== null;
  const subtitle = countsReady
    ? `${total} poddisruptionbudgets`
    : "Loading poddisruptionbudgets…";

  return (
    <div class="flex flex-col h-full">
      {/* Floating wizard modal */}
      {wizardOpen.value && (
        <PDBWizard onClose={() => (wizardOpen.value = false)} />
      )}

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
            PodDisruptionBudgets
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
          onClick={() => (wizardOpen.value = true)}
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
            stroke-width="2.5"
          >
            <path d="M4 8h8M8 4v8" />
          </svg>
          New PDB
        </button>
      </div>

      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ResourceTable
          kind="poddisruptionbudgets"
          title="PodDisruptionBudgets"
          createHref="/scaling/pdbs/new"
          hideHeader
        />
      </div>
    </div>
  );
}
