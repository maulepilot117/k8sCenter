/** @jsxImportSource react */
import { type ComponentType, useEffect, useState } from "react";
import ConfigMapWizard from "@/src/islands/ConfigMapWizard.tsx";
import ResourceTable from "@/src/islands/ResourceTable.tsx";
import SecretWizard from "@/src/islands/SecretWizard.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { formatCount, resourceCounts } from "@/src/lib/resource-counts.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

type WizardComponent = ComponentType<{ onClose: () => void }> | null;

const WIZARD_MAP: Record<string, WizardComponent> = {
  configmaps: ConfigMapWizard,
  secrets: SecretWizard,
};

function resolveTab(currentPath: string): {
  kind: string;
  title: string;
} {
  const path = currentPath.replace(/\/$/, "");

  if (path === "/config/configmaps") {
    return { kind: "configmaps", title: "ConfigMaps" };
  }

  if (path === "/config/secrets") {
    return { kind: "secrets", title: "Secrets" };
  }

  if (path === "/config/serviceaccounts") {
    return { kind: "serviceaccounts", title: "Service Accounts" };
  }

  if (path === "/config/resourcequotas") {
    return { kind: "resourcequotas", title: "Resource Quotas" };
  }

  if (path === "/config/limitranges") {
    return { kind: "limitranges", title: "Limit Ranges" };
  }

  // Default (landing /config): show ConfigMaps
  return { kind: "configmaps", title: "ConfigMaps" };
}

export default function ConfigDashboard({
  currentPath,
}: {
  currentPath: string;
}) {
  // The shared resource-counts store re-fetches when the namespace changes;
  // reading it here re-renders the subtitle with the new count.
  useSignalValue(selectedNamespace);

  const { kind, title } = resolveTab(currentPath);
  const WizardComponent = WIZARD_MAP[kind] ?? null;

  // Wizard modal — open shows the floating WizardShell
  const [wizardOpen, setWizardOpen] = useState(false);

  // Auto-open wizard when navigated with ?action=create (e.g. from CommandPalette)
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (
      new URL(globalThis.location.href).searchParams.get("action") ===
        "create" &&
      WizardComponent
    ) {
      setWizardOpen(true);
      const url = new URL(globalThis.location.href);
      url.searchParams.delete("action");
      globalThis.history.replaceState({}, "", url.toString());
    }
  }, [WizardComponent]);

  // Subtitle derived from live counts — no invented data. Secrets are never
  // counted (they stay out of the informer cache), so their count is unknown
  // and the subtitle is left out rather than reading "0 secrets".
  const countsReady = useSignalValue(resourceCounts) !== null;
  const count = formatCount(kind);

  const subtitle = !countsReady
    ? `Loading ${title.toLowerCase()}…`
    : count !== null
      ? `${count} ${title.toLowerCase()}`
      : null;

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Floating wizard modal — rendered above everything when open */}
      {wizardOpen && WizardComponent && (
        <WizardComponent onClose={() => setWizardOpen(false)} />
      )}

      {/* Page header — 24/700 per archetype spec */}
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
            {title}
          </h1>
          {subtitle && (
            <p
              style={{
                margin: "4px 0 0",
                fontSize: "13px",
                color: "var(--text-muted)",
              }}
            >
              {subtitle}
            </p>
          )}
        </div>
        {WizardComponent && (
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
            New {title.replace(/s$/, "")}
          </button>
        )}
      </div>

      {/* Content area */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ResourceTable kind={kind} title={title} hideHeader />
      </div>
    </div>
  );
}
