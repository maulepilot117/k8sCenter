import type { AppNotification } from "./notif-center-types.ts";

/**
 * Compute the in-app navigation URL for a notification based on its source
 * and resource metadata. Returns null if the notification cannot be linked.
 */
export function notifActionUrl(n: AppNotification): string | null {
  switch (n.source) {
    case "alert":
      return "/alerting";
    case "policy":
      return "/security/violations";
    case "gitops":
      return "/gitops/applications";
    case "diagnostic":
      if (n.resourceNamespace && n.resourceKind && n.resourceName) {
        return `/observability/investigate?namespace=${
          encodeURIComponent(n.resourceNamespace)
        }&kind=${encodeURIComponent(n.resourceKind)}&name=${
          encodeURIComponent(n.resourceName)
        }`;
      }
      return "/observability/investigate";
    case "scan":
      return "/security/scanning";
    case "cluster":
      return "/admin/clusters";
    case "audit":
      return "/admin/audit";
    case "limits":
      if (n.resourceNamespace) {
        return `/governance/limits/namespaces/${
          encodeURIComponent(n.resourceNamespace)
        }`;
      }
      return "/governance/limits";
    case "velero":
      return "/governance/backups";
    case "certmanager":
      if (n.resourceNamespace && n.resourceName) {
        return `/security/certificates/${
          encodeURIComponent(n.resourceNamespace)
        }/${encodeURIComponent(n.resourceName)}`;
      }
      return "/security/certificates";
    default: {
      const _exhaustive: never = n.source;
      return null;
    }
  }
}
