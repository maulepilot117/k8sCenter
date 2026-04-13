/** Gateway API types matching backend/internal/gateway/types.go */

export type GatewayResourceKind =
  | "gatewayclasses"
  | "gateways"
  | "httproutes"
  | "grpcroutes"
  | "tcproutes"
  | "tlsroutes"
  | "udproutes";

export interface GatewayAPIStatus {
  available: boolean;
  version?: string;
  installedKinds?: string[];
  lastChecked: string;
}

export interface GatewayAPISummary {
  gatewayClasses: KindSummary;
  gateways: KindSummary;
  httpRoutes: KindSummary;
  grpcRoutes: KindSummary;
  tcpRoutes: KindSummary;
  tlsRoutes: KindSummary;
  udpRoutes: KindSummary;
}

export interface KindSummary {
  total: number;
  healthy: number;
  degraded: number;
}

export interface Condition {
  type: string;
  status: string;
  reason: string;
  message: string;
  lastTransitionTime?: string;
}

export interface ParentRef {
  group: string;
  kind: string;
  name: string;
  namespace: string;
  sectionName: string;
  status: string;
  gatewayConditions?: Condition[];
}

export interface BackendRef {
  group: string;
  kind: string;
  name: string;
  namespace: string;
  port?: number;
  weight?: number;
  resolved: boolean;
}

export interface RouteSummary {
  kind: string;
  name: string;
  namespace: string;
  hostnames?: string[];
  parentRefs?: ParentRef[];
  conditions?: Condition[];
  age: string;
}

export interface GatewayClassSummary {
  name: string;
  controllerName: string;
  description?: string;
  conditions?: Condition[];
  age: string;
}

export interface Listener {
  name: string;
  port: number;
  protocol: string;
  hostname?: string;
  attachedRouteCount: number;
  tlsMode?: string;
  certificateRef?: string;
  allowedRoutes?: string;
  conditions?: Condition[];
}

export interface GatewaySummary {
  name: string;
  namespace: string;
  gatewayClassName: string;
  listeners: Listener[];
  addresses?: string[];
  attachedRouteCount: number;
  conditions?: Condition[];
  age: string;
}

export interface GatewayDetail extends GatewaySummary {
  attachedRoutes: RouteSummary[];
}

export interface HTTPRouteSummary {
  name: string;
  namespace: string;
  hostnames?: string[];
  parentRefs?: ParentRef[];
  backendCount: number;
  conditions?: Condition[];
  age: string;
}

export interface HTTPRouteMatch {
  pathType: string;
  pathValue: string;
  headers?: string[];
  method?: string;
  queryParams?: string[];
}

export interface HTTPRouteFilter {
  type: string;
  details: string;
}

export interface HTTPRouteRule {
  matches?: HTTPRouteMatch[];
  filters?: HTTPRouteFilter[];
  backendRefs?: BackendRef[];
}

export interface HTTPRouteDetail {
  name: string;
  namespace: string;
  hostnames?: string[];
  parentRefs?: ParentRef[];
  backendCount: number;
  conditions?: Condition[];
  age: string;
  rules?: HTTPRouteRule[];
}

export interface GRPCRouteMatch {
  service: string;
  method: string;
  headers?: string[];
}

export interface GRPCRouteRule {
  matches?: GRPCRouteMatch[];
  backendRefs?: BackendRef[];
}

export interface GRPCRouteDetail {
  name: string;
  namespace: string;
  parentRefs?: ParentRef[];
  rules?: GRPCRouteRule[];
  conditions?: Condition[];
  age: string;
}

export interface SimpleRouteDetail {
  kind: string;
  name: string;
  namespace: string;
  hostnames?: string[];
  parentRefs?: ParentRef[];
  backendRefs?: BackendRef[];
  conditions?: Condition[];
  age: string;
}

export type GatewayListData =
  | { kind: "gatewayclasses"; items: GatewayClassSummary[] }
  | { kind: "gateways"; items: GatewaySummary[] }
  | { kind: "httproutes"; items: HTTPRouteSummary[] }
  | {
    kind: "grpcroutes" | "tcproutes" | "tlsroutes" | "udproutes";
    items: RouteSummary[];
  };
