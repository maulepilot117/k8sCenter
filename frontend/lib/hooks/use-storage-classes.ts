import { useEffect, useState } from "react";
import { apiGet } from "@/lib/api.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";
import { hasDom } from "@/src/lib/has-dom.ts";

/** Fetches StorageClasses for dropdowns. Returns the storage class items. */
export function useStorageClasses(): StorageClassItem[] {
  const [storageClasses, setStorageClasses] = useState<StorageClassItem[]>([]);

  useEffect(() => {
    if (!hasDom()) return;
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
