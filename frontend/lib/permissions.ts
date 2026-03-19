/**
 * Permission checking utilities for frontend UI gating.
 * Sources permissions from Kubernetes RBAC via the /auth/me response.
 * This is a UX optimization — the backend still enforces on every request.
 */
import type { RBACSummary } from "@/lib/k8s-types.ts";

/**
 * Check if the user can perform a verb on a resource kind in a namespace.
 * Falls back to true if permissions are not loaded yet (optimistic until we know).
 */
export function canPerform(
  rbac: RBACSummary | null,
  kind: string,
  verb: string,
  namespace: string,
): boolean {
  if (!rbac) return true; // Permissions not loaded yet — allow optimistically

  // Check namespace-scoped permissions
  const nsPerms = rbac.namespaces?.[namespace];
  if (nsPerms) {
    const verbs = nsPerms[kind];
    if (verbs && (verbs.includes(verb) || verbs.includes("*"))) {
      return true;
    }
  }

  // Check cluster-scoped permissions (applies to all namespaces)
  const clusterPerms = rbac.clusterScoped;
  if (clusterPerms) {
    const verbs = clusterPerms[kind];
    if (verbs && (verbs.includes(verb) || verbs.includes("*"))) {
      return true;
    }
  }

  return false;
}
