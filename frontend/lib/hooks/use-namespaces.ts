import { useEffect } from "react";
import { apiGet } from "@/lib/api.ts";
import { hasDom } from "@/src/lib/has-dom.ts";
import { useSignal } from "@/src/lib/signal-hooks.ts";

/** Fetches namespace names for dropdowns. Returns a signal of sorted namespace names. */
export function useNamespaces() {
  const namespaces = useSignal<string[]>(["default"]);

  useEffect(() => {
    if (!hasDom()) return;
    apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          namespaces.value = resp.data.map((ns) => ns.metadata.name).sort();
        }
      })
      .catch(() => {});
  }, [namespaces]);

  return namespaces;
}
