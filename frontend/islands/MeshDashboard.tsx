import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { meshApi } from "@/lib/mesh-api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { MeshBadge } from "@/components/ui/MeshBadges.tsx";
import type { MeshInfo, MeshStatus } from "@/lib/mesh-types.ts";

export default function MeshDashboard() {
  const meshStatus = useSignal<MeshStatus | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      const res = await meshApi.status();
      meshStatus.value = res.data.status;
      error.value = null;
    } catch {
      error.value = "Failed to load service mesh status";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  const status = meshStatus.value;
  const istioInstalled = status?.istio?.installed === true;
  const linkerdInstalled = status?.linkerd?.installed === true;
  const noMesh = !loading.value && !error.value && !istioInstalled &&
    !linkerdInstalled;

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">Service Mesh</h1>
        {!loading.value && (
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing.value}
          >
            {refreshing.value ? "Refreshing..." : "Refresh"}
          </Button>
        )}
      </div>
      <p class="text-sm text-text-muted mb-6">
        Service mesh health — Istio &amp; Linkerd control-plane status.
      </p>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && (
        <p class="text-sm text-danger py-4">{error.value}</p>
      )}

      {/* Mesh cards — one per detected mesh */}
      {!loading.value && !error.value && (istioInstalled || linkerdInstalled) &&
        (
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mb-6">
            {istioInstalled && status?.istio && (
              <MeshInfoCard
                mesh="istio"
                info={status.istio}
                lastChecked={status.lastChecked}
              />
            )}
            {linkerdInstalled && status?.linkerd && (
              <MeshInfoCard
                mesh="linkerd"
                info={status.linkerd}
                lastChecked={status.lastChecked}
              />
            )}
          </div>
        )}

      {/* Empty state — only shown after loading completes */}
      {noMesh && (
        <div class="rounded-lg border border-border-primary p-8 text-center bg-bg-elevated">
          <p class="text-lg font-medium text-text-primary mb-2">
            No service mesh detected
          </p>
          <p class="text-sm text-text-muted mb-6">
            Install Istio or Linkerd to enable service mesh observability,
            mTLS posture tracking, and traffic routing visibility.
          </p>
          <div class="flex justify-center gap-6">
            <a
              href="https://istio.io/latest/docs/setup/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-sm text-brand hover:underline"
            >
              Install Istio &rarr;
            </a>
            <a
              href="https://linkerd.io/2/getting-started/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-sm text-brand hover:underline"
            >
              Install Linkerd &rarr;
            </a>
          </div>
        </div>
      )}

      {/* Navigation hints — only shown when mesh is present */}
      {!loading.value && !error.value && (istioInstalled || linkerdInstalled) &&
        (
          <div class="mt-6 flex items-center gap-4">
            <a
              href="/networking/mesh/routing"
              class="text-sm text-brand hover:underline"
            >
              View Mesh Routing &rarr;
            </a>
            <a
              href="/networking/mesh/mtls"
              class="text-sm text-brand hover:underline"
            >
              View mTLS Posture &rarr;
            </a>
          </div>
        )}
    </div>
  );
}

interface MeshInfoCardProps {
  mesh: "istio" | "linkerd";
  info: MeshInfo;
  lastChecked: string;
}

function MeshInfoCard({ mesh, info, lastChecked }: MeshInfoCardProps) {
  return (
    <div class="rounded-lg border border-border-primary p-5 bg-bg-elevated flex flex-col gap-3">
      <div class="flex items-center justify-between">
        <MeshBadge mesh={mesh} />
        <span class="text-xs text-text-muted">
          {new Date(lastChecked).toLocaleString()}
        </span>
      </div>
      <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
        <dt class="text-text-muted">Version</dt>
        <dd class="text-text-primary font-medium">
          {info.version || "—"}
        </dd>
        <dt class="text-text-muted">Control-plane namespace</dt>
        <dd class="text-text-primary font-medium font-mono text-xs">
          {info.namespace || "—"}
        </dd>
        {mesh === "istio" && info.mode && (
          <>
            <dt class="text-text-muted">Mode</dt>
            <dd class="text-text-primary font-medium capitalize">
              {info.mode}
            </dd>
          </>
        )}
      </dl>
    </div>
  );
}
