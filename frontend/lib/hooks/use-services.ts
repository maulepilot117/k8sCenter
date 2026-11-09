import { useEffect, useState } from "react";
import { apiGet } from "@/lib/api.ts";
import { hasDom } from "@/src/lib/has-dom.ts";

/** The route caps a page here anyway; asking for more silently gets this. */
const PAGE_LIMIT = 500;

/** One empty list, so clearing an already-empty list is not a change. */
const NONE: string[] = [];

/**
 * Service names in one namespace, for a dropdown.
 *
 * Refetches when `namespace` changes and answers with an empty list when it is
 * empty, so a caller can bind it straight to a dependent field: before a
 * namespace is chosen there is nothing to offer, which is the same shape as a
 * namespace whose services the caller cannot list.
 *
 * Empty is deliberately ALL the failure modes: a 403 (no list grant in that
 * namespace), a namespace that is gone, a namespace with no services, and a
 * transient error all leave the list empty. The caller's answer to an empty
 * list is a disabled select, which is the right answer to every one of them --
 * and specifically not a free-text fallback, since a service name that reached
 * a stored layout without coming from this list is exactly the unvalidated
 * caller text D-8 exists to keep out.
 *
 * `/v1/resources/services/{namespace}` -- the generic list route, which
 * dispatches on the resource adapter's short `Kind()`, and `services` is both
 * the adapter kind and the API resource name.
 */
export function useNamespacedServices(namespace: string): string[] {
  const [services, setServices] = useState<string[]>(NONE);

  useEffect(() => {
    if (!hasDom()) return;
    // Cleared synchronously rather than left showing the previous namespace's
    // services while the new list is in flight: a dropdown that briefly offers
    // values from somewhere else is one a fast user can pick from.
    setServices(NONE);
    if (namespace === "") return;

    const controller = new AbortController();
    // An aborted request still rejects, and it can do so after the request
    // that replaced it has already resolved. Without this flag that late
    // rejection would clear the list the new request just filled.
    let live = true;
    apiGet<Array<{ metadata?: { name?: string } }>>(
      `/v1/resources/services/${encodeURIComponent(
        namespace,
      )}?limit=${PAGE_LIMIT}`,
      controller.signal,
    )
      .then((resp) => {
        if (!live || !Array.isArray(resp.data)) return;
        setServices(
          resp.data
            .map((svc) => svc?.metadata?.name ?? "")
            .filter((name) => name !== "")
            .sort(),
        );
      })
      .catch(() => {
        if (live) setServices(NONE);
      });

    return () => {
      live = false;
      controller.abort();
    };
  }, [namespace]);

  return services;
}
