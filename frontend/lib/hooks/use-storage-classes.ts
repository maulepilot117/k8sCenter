import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";

// Inlined rather than imported from "fresh/runtime" — see lib/nav.ts for why:
// this file is shared by both the Fresh tree and the Astro/Bun port, and the
// bare specifier would otherwise resolve to the always-false test shim under
// Astro. Value-neutral for Fresh: identical to @fresh/core's own IS_BROWSER.
const IS_BROWSER = typeof document !== "undefined";

/** Fetches StorageClasses for dropdowns. Returns a signal of storage class items. */
export function useStorageClasses() {
  const storageClasses = useSignal<StorageClassItem[]>([]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<StorageClassItem[]>("/v1/resources/storageclasses?limit=500")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          storageClasses.value = resp.data;
        }
      })
      .catch(() => {});
  }, []);

  return storageClasses;
}
