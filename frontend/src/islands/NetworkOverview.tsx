/** @jsxImportSource react */
import { useEffect, useState } from "react";
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
  const [cniName, setCniName] = useState<string | null>(null);
  const [configEditable, setConfigEditable] = useState(false);
  const [activeTab, setActiveTab] = useState<"status" | "config">("status");

  // CNI info is fetched separately by CniOverview for full details.
  // This lightweight fetch only determines whether to show Cilium-specific islands.
  // Backend caches CNI detection, so the duplicate call is negligible.
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<CNIInfo>("/v1/networking/cni")
      .then((resp) => {
        setCniName(resp.data.name);
        setConfigEditable(resp.data.name === "cilium");
      })
      .catch(() => {});
  }, []);

  const isCilium = cniName === "cilium";

  return (
    <div>
      {/* Tab navigation */}
      <div className="flex gap-1 mb-6 border-b border-border-primary">
        <button
          type="button"
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            activeTab === "status"
              ? "border-brand text-brand"
              : "border-transparent text-text-muted hover:text-text-primary"
          }`}
          onClick={() => setActiveTab("status")}
        >
          Status
        </button>
        {configEditable && (
          <button
            type="button"
            className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              activeTab === "config"
                ? "border-brand text-brand"
                : "border-transparent text-text-muted hover:text-text-primary"
            }`}
            onClick={() => setActiveTab("config")}
          >
            Configuration
          </button>
        )}
      </div>

      {activeTab === "status" && (
        <div className="grid gap-4 md:grid-cols-2">
          {/* Row 1: CNI Overview + Health */}
          <CniOverview />

          {/* Cilium-specific subsystem islands */}
          {isCilium && (
            <>
              {/* Row 2: BGP + IPAM */}
              <BgpStatus />
              <IpamStatus />

              {/* Row 3: Subsystems (full-width) */}
              <div className="md:col-span-2">
                <CiliumSubsystems />
              </div>

              {/* Row 4: Node Connectivity (full-width) */}
              <div className="md:col-span-2">
                <NodeConnectivity />
              </div>
            </>
          )}

          {/* Non-Cilium message */}
          {cniName && !isCilium && (
            <div className="md:col-span-2 text-sm text-text-muted mt-2">
              Additional subsystem details are available for Cilium clusters.
            </div>
          )}
        </div>
      )}

      {activeTab === "config" && <CiliumConfigEditor />}
    </div>
  );
}
