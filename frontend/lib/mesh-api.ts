/** Typed API client for Service Mesh endpoints (Phase A + B backend).
 *
 *  Imports only from `lib/api.ts` (the underlying HTTP helpers). Endpoint shape
 *  asymmetries documented inline:
 *
 *  - GET /mesh/status returns a `{ status }` envelope (MeshStatusResponse).
 *  - GET /mesh/routing/{id} returns a bare TrafficRoute (NOT enveloped).
 *  - All other endpoints embed `status` in their response body. */

import { apiGet } from "@/lib/api.ts";
import type {
  GoldenSignalsParams,
  GoldenSignalsResponse,
  MeshStatusResponse,
  MTLSPostureResponse,
  NamespaceScope,
  RoutingResponse,
  TrafficRoute,
} from "@/lib/mesh-types.ts";

function namespaceQueryString(opts?: NamespaceScope): string {
  if (!opts?.namespace) return "";
  const p = new URLSearchParams();
  p.set("namespace", opts.namespace);
  return `?${p.toString()}`;
}

function goldenSignalsQueryString(params: GoldenSignalsParams): string {
  const p = new URLSearchParams();
  p.set("namespace", params.namespace);
  p.set("service", params.service);
  if (params.mesh) p.set("mesh", params.mesh);
  return `?${p.toString()}`;
}

export const meshApi = {
  /** Detected mesh installations (returns `{ status }` envelope). */
  status: () => apiGet<MeshStatusResponse>("/v1/mesh/status"),

  /** Traffic-routing CRDs across both meshes; `?namespace=` scopes the list. */
  routes: (opts?: NamespaceScope) =>
    apiGet<RoutingResponse>(
      `/v1/mesh/routing${namespaceQueryString(opts)}`,
    ),

  /** Single route detail. Bare TrafficRoute, no enveloping `status` field. */
  route: (id: string) =>
    apiGet<TrafficRoute>(
      `/v1/mesh/routing/${encodeURIComponent(id)}`,
    ),

  /** Per-workload mTLS posture; `?namespace=` narrows scope. */
  mtls: (opts?: NamespaceScope) =>
    apiGet<MTLSPostureResponse>(
      `/v1/mesh/mtls${namespaceQueryString(opts)}`,
    ),

  /** Per-service golden signals (RPS, error rate, latency quantiles).
   *  Wired in Phase C; consumed by Phase D's service-detail integration. */
  goldenSignals: (params: GoldenSignalsParams) =>
    apiGet<GoldenSignalsResponse>(
      `/v1/mesh/golden-signals${goldenSignalsQueryString(params)}`,
    ),
};
