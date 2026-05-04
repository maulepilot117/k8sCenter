/** Typed API client for External Secrets Operator endpoints (Phase A backend).
 *
 * Imports only from `lib/api.ts` (the underlying HTTP helpers). All endpoints
 * return enveloped responses (`{ data: T }`) per the project's API convention.
 *
 * Phase A surface: 11 read endpoints. Force-sync, bulk refresh, and other
 * write actions land in Phase E.
 */

import { apiGet, apiPost } from "@/lib/api.ts";
import type {
  BulkRefreshAction,
  BulkRefreshJob,
  BulkScopeResponse,
  ClusterExternalSecret,
  ESOStatus,
  ExternalSecret,
  PushSecret,
  SecretStore,
  StoreMetrics,
} from "@/lib/eso-types.ts";

function nsQueryString(namespace?: string): string {
  if (!namespace) return "";
  return `?namespace=${encodeURIComponent(namespace)}`;
}

function pathParam(s: string): string {
  return encodeURIComponent(s);
}

export const esoApi = {
  /** ESO discovery status — detected / namespace / version / lastChecked. */
  status: () => apiGet<ESOStatus>("/v1/externalsecrets/status"),

  /** ExternalSecrets across all visible namespaces; optional ?namespace= filter. */
  listExternalSecrets: (namespace?: string) =>
    apiGet<ExternalSecret[]>(
      `/v1/externalsecrets/externalsecrets${nsQueryString(namespace)}`,
    ),

  /** Single ExternalSecret with drift resolution. */
  getExternalSecret: (namespace: string, name: string) =>
    apiGet<ExternalSecret>(
      `/v1/externalsecrets/externalsecrets/${pathParam(namespace)}/${
        pathParam(name)
      }`,
    ),

  /** ClusterExternalSecrets — cluster-scoped, permissive-read RBAC. */
  listClusterExternalSecrets: () =>
    apiGet<ClusterExternalSecret[]>(
      "/v1/externalsecrets/clusterexternalsecrets",
    ),

  /** Single ClusterExternalSecret. */
  getClusterExternalSecret: (name: string) =>
    apiGet<ClusterExternalSecret>(
      `/v1/externalsecrets/clusterexternalsecrets/${pathParam(name)}`,
    ),

  /** Namespaced SecretStores. */
  listStores: (namespace?: string) =>
    apiGet<SecretStore[]>(
      `/v1/externalsecrets/stores${nsQueryString(namespace)}`,
    ),

  /** Single SecretStore. */
  getStore: (namespace: string, name: string) =>
    apiGet<SecretStore>(
      `/v1/externalsecrets/stores/${pathParam(namespace)}/${pathParam(name)}`,
    ),

  /** ClusterSecretStores — cluster-scoped, permissive-read RBAC. */
  listClusterStores: () =>
    apiGet<SecretStore[]>("/v1/externalsecrets/clusterstores"),

  /** Single ClusterSecretStore. */
  getClusterStore: (name: string) =>
    apiGet<SecretStore>(
      `/v1/externalsecrets/clusterstores/${pathParam(name)}`,
    ),

  /** PushSecrets — read-only in v1. */
  listPushSecrets: (namespace?: string) =>
    apiGet<PushSecret[]>(
      `/v1/externalsecrets/pushsecrets${nsQueryString(namespace)}`,
    ),

  /** Single PushSecret. */
  getPushSecret: (namespace: string, name: string) =>
    apiGet<PushSecret>(
      `/v1/externalsecrets/pushsecrets/${pathParam(namespace)}/${
        pathParam(name)
      }`,
    ),

  // --- Phase E force-sync + bulk refresh ----------------------------------

  /** Force-sync a single ExternalSecret. 202 on success, 409 already_refreshing. */
  forceSyncExternalSecret: (namespace: string, name: string) =>
    apiPost<{ status: string }>(
      `/v1/externalsecrets/externalsecrets/${pathParam(namespace)}/${
        pathParam(name)
      }/force-sync`,
    ),

  /** Resolve the visible scope for a per-store bulk refresh. */
  resolveStoreScope: (namespace: string, name: string) =>
    apiGet<BulkScopeResponse>(
      `/v1/externalsecrets/stores/${pathParam(namespace)}/${
        pathParam(name)
      }/refresh-scope`,
    ),

  /** Resolve the visible scope for a per-cluster-store bulk refresh. */
  resolveClusterStoreScope: (name: string) =>
    apiGet<BulkScopeResponse>(
      `/v1/externalsecrets/clusterstores/${pathParam(name)}/refresh-scope`,
    ),

  /** Resolve the visible scope for a per-namespace bulk refresh. */
  resolveNamespaceScope: (namespace: string) =>
    apiGet<BulkScopeResponse>(
      `/v1/externalsecrets/refresh-namespace/${
        pathParam(namespace)
      }/refresh-scope`,
    ),

  /**
   * Trigger a bulk refresh. Returns 202 + jobId. Caller polls
   * getBulkRefreshJob until completedAt is non-null. `targetUIDs` is REQUIRED
   * — pass the UIDs from a prior resolveStoreScope to pin scope. Mismatch
   * returns 409 scope_changed and the dialog can re-confirm. An empty pin
   * returns 400.
   */
  bulkRefresh: (
    action: BulkRefreshAction,
    target: { namespace?: string; name: string } | { namespace: string },
    targetUIDs: string[],
  ) => {
    let path: string;
    if (action === "refresh_store") {
      const t = target as { namespace: string; name: string };
      path = `/v1/externalsecrets/stores/${pathParam(t.namespace)}/${
        pathParam(t.name)
      }/refresh-all`;
    } else if (action === "refresh_cluster_store") {
      const t = target as { name: string };
      path = `/v1/externalsecrets/clusterstores/${
        pathParam(t.name)
      }/refresh-all`;
    } else {
      const t = target as { namespace: string };
      path = `/v1/externalsecrets/refresh-namespace/${pathParam(t.namespace)}`;
    }
    return apiPost<{ jobId: string; targetCount: number }>(
      path,
      { targetUIDs },
    );
  },

  /** Poll a bulk refresh job by id. */
  getBulkRefreshJob: (jobId: string) =>
    apiGet<BulkRefreshJob>(
      `/v1/externalsecrets/bulk-refresh-jobs/${pathParam(jobId)}`,
    ),

  /** Per-store rate + cost-tier metrics (Phase F). Always HTTP 200; the
   *  `error` field carries degradation messages so callers branch on payload
   *  shape rather than status code. */
  getStoreMetrics: (namespace: string, name: string) =>
    apiGet<StoreMetrics>(
      `/v1/externalsecrets/stores/${pathParam(namespace)}/${
        pathParam(name)
      }/metrics`,
    ),

  getClusterStoreMetrics: (name: string) =>
    apiGet<StoreMetrics>(
      `/v1/externalsecrets/clusterstores/${pathParam(name)}/metrics`,
    ),
};
