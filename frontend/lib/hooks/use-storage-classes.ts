import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";

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
