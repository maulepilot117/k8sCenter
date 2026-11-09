import { type Dispatch, type SetStateAction, useEffect, useState } from "react";
import { apiGet } from "@/lib/api.ts";
import { hasDom } from "@/src/lib/has-dom.ts";

/**
 * Fetches namespace names for dropdowns. Returns the sorted names, starting
 * from `["default"]` until the list arrives, and a setter for a caller that
 * adds a namespace it just created.
 */
export function useNamespaces(): [
  string[],
  Dispatch<SetStateAction<string[]>>,
] {
  const [namespaces, setNamespaces] = useState<string[]>(["default"]);

  useEffect(() => {
    if (!hasDom()) return;
    apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          setNamespaces(resp.data.map((ns) => ns.metadata.name).sort());
        }
      })
      .catch(() => {});
  }, []);

  return [namespaces, setNamespaces];
}
