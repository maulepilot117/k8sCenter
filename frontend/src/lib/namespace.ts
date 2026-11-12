/**
 * Shared namespace state consumed by TopBar (writes) and all resource
 * table islands (reads).
 *
 * The selection is restored from localStorage, which only the browser has, so
 * the server always sees "all". Nothing a render returns may depend on the
 * restored value until the island has hydrated: read it through
 * useAdoptSelectedNamespace, or behind useIsClient().
 */

import { useEffect } from "react";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { effect, signal } from "@/src/lib/signals.ts";

const STORAGE_KEY = "k8scenter.selectedNamespace";

const stored = IS_BROWSER ? localStorage.getItem(STORAGE_KEY) : null;

/** Currently selected namespace. "all" = all namespaces. */
export const selectedNamespace = signal<string>(stored ?? "all");

// Persist selection changes so the namespace survives page changes + reloads
// (mirrors lib/cluster.ts). `value` can be null/"" when a detail panel clears
// the selection (NamespaceLimitsDashboard), and "all" is the default — in
// those cases clear the key so a reload falls back to "all" rather than
// persisting a bogus "null"/"all" string.
if (IS_BROWSER) {
  effect(() => {
    const ns = selectedNamespace.value;
    if (ns && ns !== "all") {
      localStorage.setItem(STORAGE_KEY, ns);
    } else {
      localStorage.removeItem(STORAGE_KEY);
    }
  });
}

/**
 * The namespace a create form starts on, on the server and in the browser's
 * first render alike, so the two render the same markup.
 */
export const DEFAULT_NAMESPACE = "default";

/**
 * Once the island has hydrated, hands the namespace picker's selection to
 * `adopt` when one is selected and it is not DEFAULT_NAMESPACE. A form starts
 * on DEFAULT_NAMESPACE and moves to the picker's namespace here, because its
 * first render must match the server's, which cannot read the restored
 * selection.
 *
 * `enabled` is read once, on mount: pass false when the form's namespace came
 * from somewhere else (a URL parameter, a prop) or the resource has none.
 */
export function useAdoptSelectedNamespace(
  adopt: (namespace: string) => void,
  enabled = true,
): void {
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once, after hydration; a later picker change must not overwrite what the user chose in the form
  useEffect(() => {
    const ns = selectedNamespaceOrDefault();
    if (enabled && ns !== DEFAULT_NAMESPACE) adopt(ns);
  }, []);
}

/**
 * The picker's namespace, or DEFAULT_NAMESPACE when it is "all". It reads the
 * restored selection, so call it from an effect or a handler, or from an
 * island that renders only its useIsClient() placeholder until it has
 * hydrated. A form that renders on the server uses useAdoptSelectedNamespace.
 */
export function selectedNamespaceOrDefault(): string {
  const ns = selectedNamespace.value;
  return ns && ns !== "all" ? ns : DEFAULT_NAMESPACE;
}

/**
 * Filters a list of namespaced resources by the active namespace-picker
 * selection. Returns every item when `ns` is "all" (or empty).
 *
 * CRD-discovered dashboards fetch all namespaces (the backend list endpoints
 * return every namespace, RBAC-filtered, with no namespace query param), so
 * this is the client-side equivalent of the namespace scoping that
 * ResourceTable gets for free.
 *
 * IMPORTANT — reactivity: pass `selectedNamespace.value` explicitly from the
 * component's render path so the island re-filters when the picker changes:
 *   filterByNamespace(items, selectedNamespace.value)
 *
 * Do NOT pass cluster-scoped lists (ClusterSecretStore, ClusterExternalSecret,
 * GatewayClass, …) through this — their items have no `namespace`, so a
 * specific selection would wrongly hide all of them.
 */
export function filterByNamespace<T extends { namespace?: string }>(
  items: T[],
  ns: string,
): T[] {
  if (!ns || ns === "all") return items;
  return items.filter((item) => item.namespace === ns);
}
