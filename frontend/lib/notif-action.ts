import type { AppNotification } from "./notif-center-types.ts";

interface NotifActionOpts {
  /** When false, admin-only destinations return null. Defaults to false. */
  isAdmin?: boolean;
}

/**
 * Compute the in-app navigation URL for a notification based on its source
 * and resource metadata. Returns null if the notification cannot be linked.
 */
export function notifActionUrl(
  n: AppNotification,
  opts: NotifActionOpts = {},
): string | null {
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
      return opts.isAdmin ? "/admin/clusters" : null;
    case "audit":
      return opts.isAdmin ? "/admin/audit" : null;
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
    case "external_secrets":
      // ESO events set SuppressResourceFields=true for outbound dispatch but
      // keep namespace/name on the in-app feed (json:"-" only strips
      // Slack/webhook). The notification's ResourceKind encodes
      // "externalsecret.<EventKind>"; the navigation only needs the ES
      // coordinates so we ignore the kind suffix and link to the detail page.
      if (n.resourceNamespace && n.resourceName) {
        return `/external-secrets/external-secrets/${
          encodeURIComponent(n.resourceNamespace)
        }/${encodeURIComponent(n.resourceName)}`;
      }
      return "/external-secrets/dashboard";
    default: {
      const _exhaustive: never = n.source;
      return null;
    }
  }
}
