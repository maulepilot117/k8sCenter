/** TypeScript mirror of `backend/internal/servicemesh/types.go` and friends.
 *  Phase C consumes Phase A + B endpoints; types here track those wire shapes. */

/** MeshType — matches backend MeshType. Empty string = no mesh detected. */
export type MeshType = "" | "istio" | "linkerd" | "both";

/** MeshMode — Istio sidecar vs ambient. Empty for Linkerd or unknown. */
export type MeshMode = "" | "sidecar" | "ambient";

/** Per-workload mTLS posture. `unmeshed` distinguishes opted-out from broken. */
export type MTLSState = "active" | "inactive" | "mixed" | "unmeshed";

/** Where the posture decision came from. `default` = Linkerd default-on or no PA applied. */
export type MTLSSource = "policy" | "metric" | "default";

/** Resolved Istio PeerAuthentication mode; empty for Linkerd workloads. */
export type IstioMTLSMode =
  | ""
  | "STRICT"
  | "PERMISSIVE"
  | "DISABLE"
  | "UNSET";

/** Scope of the winning PeerAuthentication. Empty for Linkerd / UNSET / metric-driven. */
export type MTLSSourceDetail = "" | "workload" | "namespace" | "mesh";

export interface MeshInfo {
  installed: boolean;
  /** Control-plane namespace; redacted to "" for non-admin users. */
  namespace?: string;
  version?: string;
  /** Istio only; "" or absent for Linkerd. */
  mode?: MeshMode;
}

export interface MeshStatus {
  detected: MeshType;
  istio?: MeshInfo;
  linkerd?: MeshInfo;
  /** RFC 3339 timestamp. */
  lastChecked: string;
}

/** Wrapper returned by GET /mesh/status — keep symmetric with other /mesh/* endpoints. */
export interface MeshStatusResponse {
  status: MeshStatus;
}

export interface RouteDestination {
  host?: string;
  subset?: string;
  port?: number;
  weight?: number;
}

export interface RouteMatcher {
  name?: string;
  method?: string;
  pathExact?: string;
  pathPrefix?: string;
  pathRegex?: string;
}

/** Mesh-agnostic shape for routing CRDs. `mesh` + `kind` discriminate. */
export interface TrafficRoute {
  /** Composite "{mesh}:{namespace}:{kindCode}:{name}". URL-encode before embedding. */
  id: string;
  mesh: MeshType;
  kind: string;
  name: string;
  namespace?: string;
  hosts?: string[];
  gateways?: string[];
  subsets?: string[];
  /** Stringified matchLabels for Server-like resources. */
  selector?: string;
  matchers?: RouteMatcher[];
  destinations?: RouteDestination[];
  /** Full unstructured spec, surfaced verbatim for the YAML viewer. */
  raw?: Record<string, unknown>;
}

export interface RoutingResponse {
  status: MeshStatus;
  routes: TrafficRoute[];
  /** Per-CRD or per-stage error keys, e.g. "istio/VirtualService". */
  errors?: Record<string, string>;
}

export interface MeshedPolicy {
  id: string;
  mesh: MeshType;
  kind: string;
  name: string;
  namespace?: string;
  /** Mesh-native action (ALLOW / DENY / AUDIT / CUSTOM / ...). */
  action?: string;
  /** Computed corner cases: "deny_all" | "allow_all" | "". */
  effect?: string;
  /** PeerAuthentication only: STRICT / PERMISSIVE / DISABLE / UNSET. */
  mtlsMode?: IstioMTLSMode;
  selector?: string;
  ruleCount: number;
  raw?: Record<string, unknown>;
}

export interface PoliciesResponse {
  status: MeshStatus;
  policies: MeshedPolicy[];
  errors?: Record<string, string>;
}

export interface WorkloadMTLS {
  namespace: string;
  workload: string;
  /** Authoritative when `workloadKindConfident` is true; heuristic-derived otherwise. */
  workloadKind?: string;
  mesh: MeshType;
  state: MTLSState;
  source: MTLSSource;
  /** Empty for Linkerd. */
  istioMode?: IstioMTLSMode;
  /** Empty for Linkerd / UNSET / metric-driven decisions. */
  sourceDetail?: MTLSSourceDetail;
  /**
   * `false` means `workloadKind` was inferred from the ReplicaSet name
   * via the kube-controller pod-template-hash heuristic (no owner-ref
   * lookup). UI clients should mark non-confident rows visually.
   */
  workloadKindConfident: boolean;
}

export interface MTLSPostureResponse {
  status: MeshStatus;
  workloads: WorkloadMTLS[];
  /** Partial-failure surface: `pods`, `policies`, `truncated`, `prometheus-cross-check`, `istio/VirtualService`, etc. */
  errors?: Record<string, string>;
}

/** Per-service golden signals. Phase C wires the API client; Phase D consumes it. */
export interface GoldenSignals {
  mesh: MeshType;
  namespace: string;
  service: string;
  /** False when the metrics subsystem is unavailable. */
  available: boolean;
  /** Populated only when `available` is false. */
  reason?: string;
  rps: number;
  /** Fraction in [0, 1]. */
  errorRate: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
}

export interface GoldenSignalsResponse {
  status: MeshStatus;
  signals: GoldenSignals;
}

/** Optional query-param shape for the namespace-scoped list endpoints. */
export interface NamespaceScope {
  namespace?: string;
}

/** Required params for the golden-signals endpoint. */
export interface GoldenSignalsParams {
  namespace: string;
  service: string;
  /** Required only when both meshes are installed; backend auto-detects otherwise. */
  mesh?: "istio" | "linkerd";
}
