import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import BgpStatus from "@/src/islands/BgpStatus.tsx";
import CiliumConfigEditor from "@/src/islands/CiliumConfigEditor.tsx";
import CiliumSubsystems from "@/src/islands/CiliumSubsystems.tsx";
import CniOverview from "@/src/islands/CniOverview.tsx";
import IpamStatus from "@/src/islands/IpamStatus.tsx";
import NodeConnectivity from "@/src/islands/NodeConnectivity.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

interface CNIInfo {
  name: string;
}

export default function NetworkOverview() {
  const cniName = useSignal<string | null>(null);
  const configEditable = useSignal(false);
  const activeTab = useSignal<"status" | "config">("status");

  // CNI info is fetched separately by CniOverview for full details.
  // This lightweight fetch only determines whether to show Cilium-specific islands.
  // Backend caches CNI detection, so the duplicate call is negligible.
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<CNIInfo>("/v1/networking/cni")
      .then((resp) => {
        cniName.value = resp.data.name;
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
          {/* Row 1: CNI Overview + Health */}
          <CniOverview />

          {/* Cilium-specific subsystem islands */}
          {isCilium && (
            <>
              {/* Row 2: BGP + IPAM */}
              <BgpStatus />
              <IpamStatus />

              {/* Row 3: Subsystems (full-width) */}
              <div class="md:col-span-2">
                <CiliumSubsystems />
              </div>

              {/* Row 4: Node Connectivity (full-width) */}
              <div class="md:col-span-2">
                <NodeConnectivity />
              </div>
            </>
          )}

          {/* Non-Cilium message */}
          {cniName.value && !isCilium && (
            <div class="md:col-span-2 text-sm text-text-muted mt-2">
              Additional subsystem details are available for Cilium clusters.
            </div>
          )}
        </div>
      )}

      {activeTab.value === "config" && <CiliumConfigEditor />}
    </div>
  );
}
