import type { K8sEvent } from "@/lib/k8s-types.ts";

/**
 * Short forms for the noisiest resource kinds, so an event row reads
 * `deploy/api` rather than `deployment/api`.
 *
 * Module scope, not inside a row loop: the pre-registry island rebuilt this
 * object once per event on every render.
 */
export const KIND_ABBR: Record<string, string> = {
  deployment: "deploy",
  service: "svc",
  replicaset: "rs",
  statefulset: "sts",
  daemonset: "ds",
  persistentvolumeclaim: "pvc",
  horizontalpodautoscaler: "hpa",
  configmap: "cm",
  serviceaccount: "sa",
  networkpolicy: "netpol",
};

/**
 * The `kind/name` prefix shown at the head of an event row, or an empty string
 * when the event names no object.
 *
 * An unmapped kind falls through to its own lowercased name rather than being
 * dropped, so a new resource kind renders readably without a table entry.
 * Callers treat the empty string as "render no prefix".
 */
export function formatEventLabel(evt: K8sEvent): string {
  const name = evt.involvedObject?.name;
  if (!name) return "";
  const kind = evt.involvedObject?.kind?.toLowerCase() ?? "";
  const prefix = KIND_ABBR[kind] ?? kind;
  return `${prefix}/${name}`;
}

/**
 * The event list as an array, whatever the cache handed over.
 *
 * An endpoint returning no body leaves the cached `data` as `undefined`, and
 * WidgetHost's render gate only rejects `null`, so an undefined payload does
 * reach the widget. The pre-registry island guarded with Array.isArray before
 * assigning, for the same reason.
 */
export function asEventList(raw: unknown): K8sEvent[] {
  return Array.isArray(raw) ? (raw as K8sEvent[]) : [];
}
