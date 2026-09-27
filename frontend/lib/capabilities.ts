/**
 * Client for GET /api/v1/capabilities/{clusterID} (U8), and the operator-facing
 * reading of its rows (U11b).
 *
 * Client-only module — MUST NOT be imported in server-rendered components. The
 * cache below holds per-identity permission answers in module scope; imported
 * during SSR it would hand one user's answers to the next request. It imports
 * lib/api.ts, so `server/check-no-signal-store-in-ssr.ts` already fails the
 * build if a non-island SSR path ever reaches it.
 *
 * **Caching.** The server computes the response per request and sends
 * `Cache-Control: no-store`; it carries no expiry of its own. The cache here is
 * a short client-side TTL on top of that, keyed on (cluster id, generation) so
 * a delete-and-re-register under a recycled id is never served the previous
 * registration's answers. A cluster switch drops every entry — the epoch means
 * "the operator moved", and a capability read issued after a move must reflect
 * the world as it is now. A logout or identity change is a full page load, so
 * the module state, cache included, does not survive it.
 */
import { apiGet } from "./api.ts";
import type {
  CapabilitiesResponse,
  Capability,
  CapabilityOperationId,
  ReasonCode,
} from "./capability-types.ts";
import { type ClusterTarget, clusterEpoch } from "./cluster.ts";

/**
 * How long a capability answer is reused. Short on purpose: the inputs it
 * summarises change on the order of a minute (the 60s reachability probe, the
 * 60s SAR cache behind `authorized`), so holding an answer longer than that
 * would make the banner lag the server's own view.
 */
export const CAPABILITY_TTL_MS = 30_000;

interface CacheEntry {
  expiresAt: number;
  value: CapabilitiesResponse;
}

const cache = new Map<string, CacheEntry>();
let cacheEpoch = clusterEpoch.peek();

function cacheKey(target: ClusterTarget): string {
  return `${target.clusterId}\0${target.generation}`;
}

/** Drops every entry once the epoch has moved since they were written. */
function syncCacheEpoch(): number {
  const epoch = clusterEpoch.peek();
  if (epoch !== cacheEpoch) {
    cache.clear();
    cacheEpoch = epoch;
  }
  return epoch;
}

/**
 * Reads what the current identity can do against `target`.
 *
 * The request is addressed to `target.clusterId` explicitly rather than to the
 * ambient selection. The handler 409s when the X-Cluster-ID header and the path
 * disagree, and the selection may have moved between the caller capturing its
 * target and this call issuing the request.
 *
 * A response naming a different cluster than the one asked about is rejected
 * rather than cached: rendering it would describe the wrong cluster.
 */
export async function fetchCapabilities(
  target: ClusterTarget,
  signal?: AbortSignal,
): Promise<CapabilitiesResponse> {
  const issuedEpoch = syncCacheEpoch();
  const key = cacheKey(target);
  const hit = cache.get(key);
  if (hit && hit.expiresAt > Date.now()) return hit.value;
  cache.delete(key);

  const res = await apiGet<CapabilitiesResponse>(
    `/v1/capabilities/${encodeURIComponent(target.clusterId)}`,
    { clusterId: target.clusterId, signal },
  );
  const value = res.data;
  if (value?.clusterId !== target.clusterId) {
    throw new Error(
      `capabilities response describes cluster "${value?.clusterId}", not "${target.clusterId}"`,
    );
  }

  // A switch that landed while this was in flight has already invalidated
  // the cache; writing into it now would resurrect a pre-switch answer.
  if (syncCacheEpoch() === issuedEpoch) {
    cache.set(key, { expiresAt: Date.now() + CAPABILITY_TTL_MS, value });
  }
  return value;
}

/** The row for one operation, if the server published one. */
export function capabilityFor(
  caps: CapabilitiesResponse,
  operation: CapabilityOperationId,
): Capability | undefined {
  return caps.capabilities.find((c) => c.operation === operation);
}

/**
 * How a capability row is presented.
 *
 * - `ok`          — nothing to say.
 * - `blocked`     — k8sCenter supports it, but something about this cluster or
 *                   this identity stops it right now. Usually recoverable.
 * - `unsupported` — k8sCenter has not implemented it for this kind of cluster.
 *                   Reserved for `unsupported_platform` alone (D3): a cluster
 *                   that is down, or an account without RBAC, must never be
 *                   described as something the product cannot do.
 * - `unknown`     — the answer could not be determined. Never rendered as
 *                   permitted.
 */
export type CapabilityTone = "ok" | "blocked" | "unsupported" | "unknown";

export interface CapabilityExplanation {
  tone: CapabilityTone;
  message: string;
}

export function explain(cap: Capability): CapabilityExplanation {
  const what = cap.label || cap.operation;
  const code: ReasonCode = cap.reasonCode;
  switch (code) {
    case "ok":
      return { tone: "ok", message: `${what} is available on this cluster.` };
    case "unsupported_platform":
      return {
        tone: "unsupported",
        message: `k8sCenter does not support ${what} on this kind of cluster.`,
      };
    case "discovery_missing":
      return {
        tone: "blocked",
        message: `This cluster does not serve the API that ${what} needs.`,
      };
    case "discovery_unavailable":
      return {
        tone: "unknown",
        message: `Could not read this cluster's API list, so it is not known whether ${what} will work.`,
      };
    case "unreachable":
      return {
        tone: "blocked",
        message: `This cluster is not responding. ${what} will fail until it is reachable again.`,
      };
    case "stale_observation":
      return {
        tone: "unknown",
        message:
          "This cluster has not been checked recently, so it is not known whether it is reachable.",
      };
    case "forbidden":
      return {
        tone: "blocked",
        message: `Your account does not have permission for ${what} on this cluster.`,
      };
    case "authz_unknown":
      return {
        tone: "unknown",
        message: `Could not check whether your account has permission for ${what}.`,
      };
    case "authz_namespace_scoped":
      return {
        tone: "unknown",
        message: `Your account does not have ${what} permission across the whole cluster. It may still work in namespaces where you have access.`,
      };
    case "cluster_unknown":
      return {
        tone: "blocked",
        message: "This cluster is no longer registered with k8sCenter.",
      };
    case "credentials_invalid":
      return {
        tone: "blocked",
        message:
          "k8sCenter's stored credentials for this cluster are not working. An admin needs to update them.",
      };
    case "db_unavailable":
      return {
        tone: "unknown",
        message:
          "Could not read the cluster registry, so this cluster's status is not known.",
      };
    default: {
      // Every ReasonCode is handled above, so `code` is `never` here and a
      // new member added to REASON_CODES fails to compile until it is given
      // copy. At runtime a newer backend can still send a code this build has
      // never seen; that is an answer we cannot interpret, not a permission.
      const unhandled: never = code;
      return {
        tone: "unknown",
        message: `Could not determine whether ${what} is available (${String(unhandled)}).`,
      };
    }
  }
}
