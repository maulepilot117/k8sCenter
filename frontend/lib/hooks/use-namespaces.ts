import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";

// Inlined rather than imported from "fresh/runtime" — see lib/nav.ts for why:
// this file is shared by both the Fresh tree and the Astro/Bun port, and the
// bare specifier would otherwise resolve to the always-false test shim under
// Astro. Value-neutral for Fresh: identical to @fresh/core's own IS_BROWSER.
const IS_BROWSER = typeof document !== "undefined";

/** Fetches namespace names for dropdowns. Returns a signal of sorted namespace names. */
export function useNamespaces() {
  const namespaces = useSignal<string[]>(["default"]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          namespaces.value = resp.data.map((ns) => ns.metadata.name).sort();
        }
      })
      .catch(() => {});
  }, []);

  return namespaces;
}
