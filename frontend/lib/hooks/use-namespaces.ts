import { useEffect } from "react";
import { apiGet } from "@/lib/api.ts";
import { useSignal } from "@/src/lib/signal-hooks.ts";

/** Fetches namespace names for dropdowns. Returns a signal of sorted namespace names. */
export function useNamespaces() {
  const namespaces = useSignal<string[]>(["default"]);

  useEffect(() => {
    // Checked when the effect runs, not once at module load: a module first
    // imported where there is no DOM (a server render, a test) would
    // otherwise keep answering "no browser" for the rest of the process.
    if (typeof document === "undefined") return;
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
