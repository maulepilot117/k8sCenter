import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import CniOverview from "@/islands/CniOverview.tsx";
import CiliumConfigEditor from "@/islands/CiliumConfigEditor.tsx";

interface CNIInfo {
  name: string;
  features?: {
    hubble: boolean;
    encryption: boolean;
    encryptionMode: string;
    clusterMesh: boolean;
    wireguard: boolean;
    envoyEnabled: boolean;
  };
}

export default function NetworkOverview() {
  const cniName = useSignal<string | null>(null);
  const configEditable = useSignal(false);
  const activeTab = useSignal<"status" | "config">("status");

  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<CNIInfo>("/v1/networking/cni")
      .then((resp) => {
        cniName.value = resp.data.name;
        // Config tab is only available for Cilium
        configEditable.value = resp.data.name === "cilium";
      })
      .catch(() => {});
  }, []);

  const isCilium = cniName.value === "cilium";

  return (
    <div>
      {/* Tab navigation */}
      <div class="flex gap-1 mb-6 border-b border-border-primary">
        <button
          type="button"
          class={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            activeTab.value === "status"
              ? "border-brand text-brand"
              : "border-transparent text-text-muted hover:text-text-primary"
          }`}
          onClick={() => (activeTab.value = "status")}
        >
          Status
        </button>
        {configEditable.value && (
          <button
            type="button"
            class={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              activeTab.value === "config"
                ? "border-brand text-brand"
                : "border-transparent text-text-muted hover:text-text-primary"
            }`}
            onClick={() => (activeTab.value = "config")}
          >
            Configuration
          </button>
        )}
      </div>

      {activeTab.value === "status" && (
        <div class="grid gap-4 md:grid-cols-2">
          {/* Row 1: CNI Overview + Health (fragment renders 2 grid cells) */}
          <CniOverview />

          {/* Cilium-specific subsystem islands */}
          {isCilium && <CiliumSubsystemPlaceholder />}
        </div>
      )}

      {activeTab.value === "config" && <CiliumConfigEditor />}
    </div>
  );
}

/**
 * Placeholder for Cilium subsystem islands (BGP, IPAM, Subsystems).
 * Replaced in Step 5 with actual island imports.
 */
function CiliumSubsystemPlaceholder() {
  return (
    <>
      <div
        class="rounded-lg border p-6 opacity-50"
        style={{
          background: "var(--bg-surface)",
          borderColor: "var(--border-primary)",
        }}
      >
        <h3
          class="mb-4 text-sm font-semibold uppercase tracking-wider"
          style={{ color: "var(--text-muted)" }}
        >
          BGP Peering
        </h3>
        <p class="text-sm" style={{ color: "var(--text-muted)" }}>
          Loading...
        </p>
      </div>
      <div
        class="rounded-lg border p-6 opacity-50"
        style={{
          background: "var(--bg-surface)",
          borderColor: "var(--border-primary)",
        }}
      >
        <h3
          class="mb-4 text-sm font-semibold uppercase tracking-wider"
          style={{ color: "var(--text-muted)" }}
        >
          IP Address Management
        </h3>
        <p class="text-sm" style={{ color: "var(--text-muted)" }}>
          Loading...
        </p>
      </div>
      <div
        class="md:col-span-2 rounded-lg border p-6 opacity-50"
        style={{
          background: "var(--bg-surface)",
          borderColor: "var(--border-primary)",
        }}
      >
        <h3
          class="mb-4 text-sm font-semibold uppercase tracking-wider"
          style={{ color: "var(--text-muted)" }}
        >
          Subsystems
        </h3>
        <p class="text-sm" style={{ color: "var(--text-muted)" }}>
          Loading...
        </p>
      </div>
    </>
  );
}
