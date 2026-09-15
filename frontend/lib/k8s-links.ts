import { RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";

/** Irregular plurals for resource kind -> RESOURCE_DETAIL_PATHS lookup. */
const KIND_PLURALS: Record<string, string> = {
  ingress: "ingresses",
  endpointslice: "endpointslices",
  networkpolicy: "networkpolicies",
};

/**
 * Build an href to a resource detail page, or null if the kind is unknown.
 *
 * Accepts either a singular Kubernetes kind ("Service", "secret") or an
 * adapter slug, which is already plural ("configmaps", "endpoints"). Pins
 * store the slug, because that is the form the preferences API validates
 * against, and blind pluralization turns "configmaps" into "configmapss" —
 * every pinned row would then render as unroutable. So an exact path key
 * wins before any pluralization is attempted.
 */
export function resourceHref(
  kind: string,
  namespace?: string,
  name?: string,
): string | null {
  const lower = kind.toLowerCase();
  const plural = RESOURCE_DETAIL_PATHS[lower]
    ? lower
    : (KIND_PLURALS[lower] ?? `${lower}s`);
  const basePath = RESOURCE_DETAIL_PATHS[plural];
  if (!basePath || !name) return null;
  return namespace
    ? `${basePath}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`
    : `${basePath}/${encodeURIComponent(name)}`;
}
