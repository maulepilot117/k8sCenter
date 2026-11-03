import { type ReadonlySignal, useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID } from "@/src/lib/cluster.ts";

/** The fields of a cluster record a label needs. */
export interface ClusterRecord {
  id: string;
  name: string;
  displayName?: string;
}

/**
 * The display label of a cluster: "the local cluster" for local, and a
 * remote cluster's registered name from `names`. Remote ids are opaque, but
 * an id is still an unambiguous label, so it stands in until the name is
 * known or when the record cannot be read.
 */
export function clusterLabel(
  id: string,
  names: ReadonlyMap<string, string>,
): string {
  if (id === LOCAL_CLUSTER_ID) return "the local cluster";
  return names.get(id) ?? id;
}

/**
 * The registered names of the remote clusters in `ids`, read from the
 * registry once each and kept for the component's life. Best effort: a
 * cluster whose record cannot be read is left out, and `clusterLabel` falls
 * back to its id.
 */
export function useClusterNames(
  ids: readonly (string | undefined)[],
): ReadonlySignal<ReadonlyMap<string, string>> {
  const names = useSignal<ReadonlyMap<string, string>>(new Map());
  const wanted = [
    ...new Set(
      ids.filter((id): id is string => !!id && id !== LOCAL_CLUSTER_ID),
    ),
  ];
  const key = wanted.join("\n");
  useEffect(() => {
    const known = names.peek();
    const controller = new AbortController();
    for (const id of wanted) {
      if (known.has(id)) continue;
      apiGet<ClusterRecord>(`/v1/clusters/${encodeURIComponent(id)}`, {
        signal: controller.signal,
      })
        .then((res) => {
          const c = res.data;
          if (!c) return;
          const next = new Map(names.peek());
          next.set(id, c.displayName || c.name || id);
          names.value = next;
        })
        .catch(() => {});
    }
    return () => controller.abort();
  }, [key]);
  return names;
}
