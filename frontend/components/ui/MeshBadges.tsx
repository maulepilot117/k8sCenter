/** Badge components for Service Mesh views (mesh, mTLS state, mTLS source, kind).
 *
 *  Color sourcing: `lib/badge-colors.ts` covers the SEVERITY scale
 *  (`critical`/`high`/`medium`/`low`). The mesh-specific keys below don't fit
 *  that scale (`active`/`inactive`/`mixed`/`unmeshed` is a posture taxonomy,
 *  not a severity ranking), so they live here next to the badges that use
 *  them. All values resolve to the shared `var(--…)` theme tokens. */

import { ColorBadge } from "@/components/ui/ColorBadge.tsx";
import type { MeshType, MTLSSource, MTLSState } from "@/lib/mesh-types.ts";

export const MESH_COLORS: Record<string, string> = {
  istio: "var(--accent)",
  linkerd: "var(--success)",
  both: "var(--accent-secondary)",
};

export const MTLS_STATE_COLORS: Record<MTLSState, string> = {
  active: "var(--success)",
  mixed: "var(--warning)",
  inactive: "var(--danger)",
  unmeshed: "var(--text-muted)",
};

export const MTLS_SOURCE_COLORS: Record<MTLSSource, string> = {
  policy: "var(--accent)",
  metric: "var(--accent-secondary)",
  default: "var(--text-muted)",
};

/** Human-readable labels for the kind code embedded in TrafficRoute composite IDs. */
export const KIND_LABELS: Record<string, string> = {
  vs: "VirtualService",
  dr: "DestinationRule",
  gw: "Gateway",
  pa: "PeerAuthentication",
  ap: "AuthorizationPolicy",
  sp: "ServiceProfile",
  srv: "Server",
  hr: "HTTPRoute",
  mtls: "MeshTLSAuthentication",
};

export function MeshBadge({ mesh }: { mesh: MeshType }) {
  const labels: Record<string, string> = {
    istio: "Istio",
    linkerd: "Linkerd",
    both: "Multi-mesh",
    "": "Unknown",
  };
  return (
    <ColorBadge
      label={labels[mesh] ?? mesh}
      color={MESH_COLORS[mesh] ?? "var(--text-muted)"}
    />
  );
}

export function MTLSStateBadge({ state }: { state: MTLSState }) {
  const labels: Record<MTLSState, string> = {
    active: "Active",
    mixed: "Mixed",
    inactive: "Inactive",
    unmeshed: "Unmeshed",
  };
  return <ColorBadge label={labels[state]} color={MTLS_STATE_COLORS[state]} />;
}

export function MTLSSourceBadge({ source }: { source: MTLSSource }) {
  const labels: Record<MTLSSource, string> = {
    policy: "Policy",
    metric: "Metric",
    default: "Default",
  };
  return (
    <ColorBadge label={labels[source]} color={MTLS_SOURCE_COLORS[source]} />
  );
}

/** KindBadge maps a composite-ID kind code or the raw `Kind` string to a label. */
export function KindBadge({ kind }: { kind: string }) {
  const lower = kind.toLowerCase();
  const label = KIND_LABELS[lower] ?? kind;
  return <ColorBadge label={label} color="var(--text-muted)" />;
}
