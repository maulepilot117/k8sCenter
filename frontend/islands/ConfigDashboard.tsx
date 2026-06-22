import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { selectedNamespace } from "@/lib/namespace.ts";
import { getCount, resourceCounts } from "@/lib/resource-counts.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";
import ConfigMapWizard from "@/islands/ConfigMapWizard.tsx";
import SecretWizard from "@/islands/SecretWizard.tsx";

type WizardComponent =
  | (({ onClose }: { onClose: () => void }) => preact.JSX.Element)
  | null;

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

export default function ConfigDashboard(
  { currentPath }: { currentPath: string },
) {
  // Reading selectedNamespace.value here wires reactivity — the shared
  // resource-counts store re-fetches when namespace changes.
  const _ns = selectedNamespace.value;

  const { kind, title } = resolveTab(currentPath);
  const WizardComponent = WIZARD_MAP[kind] ?? null;

  // Wizard modal signal — open=true shows the floating WizardShell
  const wizardOpen = useSignal(false);

  // Auto-open wizard when navigated with ?action=create (e.g. from CommandPalette)
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (
      new URL(globalThis.location.href).searchParams.get("action") ===
        "create" && WizardComponent
    ) {
      wizardOpen.value = true;
      const url = new URL(globalThis.location.href);
      url.searchParams.delete("action");
      globalThis.history.replaceState({}, "", url.toString());
    }
  }, []);

  // Subtitle derived from live counts — no invented data.
  const total = getCount(kind) ?? 0;
  const countsReady = resourceCounts.value !== null;

  const subtitle = countsReady
    ? `${total} ${title.toLowerCase()}`
    : `Loading ${title.toLowerCase()}…`;

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Floating wizard modal — rendered above everything when open */}
      {wizardOpen.value && WizardComponent && (
        <WizardComponent onClose={() => (wizardOpen.value = false)} />
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
        {WizardComponent && (
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
            New {title.replace(/s$/, "")}
          </button>
        )}
      </div>

      {/* Content area */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ResourceTable
          kind={kind}
          title={title}
          hideHeader
        />
      </div>
    </div>
  );
}
