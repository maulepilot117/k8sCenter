import { useEffect, useState } from "react";
import { apiGet } from "@/lib/api.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";

/** Fetches StorageClasses for dropdowns. Returns the storage class items. */
export function useStorageClasses(): StorageClassItem[] {
  const [storageClasses, setStorageClasses] = useState<StorageClassItem[]>([]);

  useEffect(() => {
    // Checked when the effect runs, not once at module load: a module first
    // imported where there is no DOM (a server render, a test) would
    // otherwise keep answering "no browser" for the rest of the process.
    if (typeof document === "undefined") return;
    apiGet<StorageClassItem[]>("/v1/resources/storageclasses?limit=500")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          setStorageClasses(resp.data);
        }
      })
      .catch(() => {});
  }, []);

  return storageClasses;
}
