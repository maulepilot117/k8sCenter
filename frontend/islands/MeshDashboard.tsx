import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { meshApi } from "@/lib/mesh-api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { MeshBadge } from "@/components/ui/MeshBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
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
    <div style={{ padding: "24px" }}>
      {/* Page header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: "4px",
        }}
      >
        <h1
          style={{
            margin: 0,
            fontSize: "24px",
            fontWeight: 700,
            color: "var(--text-primary)",
          }}
        >
          Service Mesh
        </h1>
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
      <p
        style={{
          margin: "0 0 24px",
          fontSize: "13px",
          color: "var(--text-muted)",
        }}
      >
        Service mesh health — Istio &amp; Linkerd control-plane status.
      </p>

      {/* Loading state */}
      {loading.value && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            paddingTop: "48px",
            paddingBottom: "48px",
          }}
        >
          <Spinner />
        </div>
      )}

      {/* Error state */}
      {error.value && (
        <p
          style={{
            fontSize: "13px",
            color: "var(--error)",
            paddingTop: "16px",
            paddingBottom: "16px",
          }}
        >
          {error.value}
        </p>
      )}

      {/* Mesh cards — one per detected mesh */}
      {!loading.value && !error.value && (istioInstalled || linkerdInstalled) &&
        (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(320px, 1fr))",
              gap: "16px",
              marginBottom: "24px",
            }}
          >
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

      {/* Empty state */}
      {noMesh && (
        <WidgetShell style={{ textAlign: "center" }}>
          <p
            style={{
              margin: "0 0 8px",
              fontSize: "16px",
              fontWeight: 600,
              color: "var(--text-primary)",
            }}
          >
            No service mesh detected
          </p>
          <p
            style={{
              margin: "0 0 24px",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Install Istio or Linkerd to enable service mesh observability, mTLS
            posture tracking, and traffic routing visibility.
          </p>
          <div
            style={{
              display: "flex",
              justifyContent: "center",
              gap: "24px",
            }}
          >
            <a
              href="https://istio.io/latest/docs/setup/"
              target="_blank"
              rel="noopener noreferrer"
              style={{
                fontSize: "13px",
                color: "var(--accent)",
                textDecoration: "none",
              }}
            >
              Install Istio &rarr;
            </a>
            <a
              href="https://linkerd.io/2/getting-started/"
              target="_blank"
              rel="noopener noreferrer"
              style={{
                fontSize: "13px",
                color: "var(--accent)",
                textDecoration: "none",
              }}
            >
              Install Linkerd &rarr;
            </a>
          </div>
        </WidgetShell>
      )}

      {/* Navigation hints — only shown when mesh is present */}
      {!loading.value && !error.value && (istioInstalled || linkerdInstalled) &&
        (
          <div
            style={{
              marginTop: "24px",
              display: "flex",
              alignItems: "center",
              gap: "16px",
            }}
          >
            <a
              href="/networking/mesh/routing"
              style={{
                fontSize: "13px",
                color: "var(--accent)",
                textDecoration: "none",
              }}
            >
              View Mesh Routing &rarr;
            </a>
            <a
              href="/networking/mesh/mtls"
              style={{
                fontSize: "13px",
                color: "var(--accent)",
                textDecoration: "none",
              }}
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
  const meshLabel = mesh === "istio" ? "Istio" : "Linkerd";

  return (
    <WidgetShell
      title={meshLabel}
      action={
        <span style={{ fontSize: "11px", color: "var(--text-muted)" }}>
          {new Date(lastChecked).toLocaleString()}
        </span>
      }
    >
      {/* Mesh identity badge row */}
      <div style={{ marginBottom: "14px" }}>
        <MeshBadge mesh={mesh} />
      </div>

      {/* Key-value grid — solid, not glass */}
      <dl
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          columnGap: "16px",
          rowGap: "8px",
          margin: 0,
        }}
      >
        <dt
          style={{
            fontSize: "11px",
            fontWeight: 600,
            textTransform: "uppercase",
            letterSpacing: "0.04em",
            color: "var(--text-muted)",
            margin: 0,
          }}
        >
          Version
        </dt>
        <dd
          style={{
            fontSize: "13px",
            fontWeight: 500,
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          {info.version || "—"}
        </dd>

        <dt
          style={{
            fontSize: "11px",
            fontWeight: 600,
            textTransform: "uppercase",
            letterSpacing: "0.04em",
            color: "var(--text-muted)",
            margin: 0,
          }}
        >
          Namespace
        </dt>
        <dd
          style={{
            fontSize: "12px",
            fontWeight: 500,
            fontFamily: "monospace",
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          {info.namespace || "—"}
        </dd>

        {mesh === "istio" && info.mode && (
          <>
            <dt
              style={{
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.04em",
                color: "var(--text-muted)",
                margin: 0,
              }}
            >
              Mode
            </dt>
            <dd
              style={{
                fontSize: "13px",
                fontWeight: 500,
                color: "var(--text-primary)",
                textTransform: "capitalize",
                margin: 0,
              }}
            >
              {info.mode}
            </dd>
          </>
        )}
      </dl>
    </WidgetShell>
  );
}
