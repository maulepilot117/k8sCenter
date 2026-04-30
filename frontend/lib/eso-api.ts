/** Typed API client for External Secrets Operator endpoints (Phase A backend).
 *
 * Imports only from `lib/api.ts` (the underlying HTTP helpers). All endpoints
 * return enveloped responses (`{ data: T }`) per the project's API convention.
 *
 * Phase A surface: 11 read endpoints. Force-sync, bulk refresh, and other
 * write actions land in Phase E.
 */

import { apiGet } from "@/lib/api.ts";
import type {
  ClusterExternalSecret,
  ESOStatus,
  ExternalSecret,
  PushSecret,
  SecretStore,
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
};
