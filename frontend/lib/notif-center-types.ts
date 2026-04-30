/** Notification severity levels matching backend notifications.Severity */
export type NotifSeverity = "critical" | "warning" | "info";

/** Notification source subsystems matching backend notifications.Source */
export type NotifSource =
  | "alert"
  | "policy"
  | "gitops"
  | "diagnostic"
  | "scan"
  | "cluster"
  | "audit"
  | "limits"
  | "velero"
  | "certmanager"
  | "external_secrets";

/** Notification channel types matching backend notifications.ChannelType */
export type NotifChannelType = "slack" | "email" | "webhook";

/** A single notification from the backend feed. */
export interface AppNotification {
  id: string;
  source: NotifSource;
  severity: NotifSeverity;
  title: string;
  message: string;
  resourceKind?: string;
  resourceNamespace?: string;
  resourceName?: string;
  clusterId?: string;
  createdAt: string;
  /** Absent means unread (Go omitempty omits false). Treat undefined as false. */
  read?: boolean;
}

/** External dispatch channel configuration. */
export interface NotifChannel {
  id: string;
  name: string;
  type: NotifChannelType;
  config: Record<string, unknown>;
  createdBy: string;
  createdAt: string;
  updatedAt?: string;
  updatedBy?: string;
  lastSentAt?: string;
  lastError?: string;
  lastErrorAt?: string;
}

/** Routing rule that maps notifications to channels. */
export interface NotifRule {
  id: string;
  name: string;
  sourceFilter: NotifSource[];
  severityFilter: NotifSeverity[];
  channelId: string;
  channelName?: string;
  enabled: boolean;
  createdBy: string;
  createdAt: string;
  updatedAt?: string;
  updatedBy?: string;
}

/** Input for creating/updating a channel (only mutable fields). */
export interface NotifChannelInput {
  name: string;
  type: NotifChannelType;
  config: Record<string, unknown>;
}

/** Input for creating/updating a rule (only mutable fields). */
export interface NotifRuleInput {
  name: string;
  sourceFilter: NotifSource[];
  severityFilter: NotifSeverity[];
  channelId: string;
  enabled: boolean;
}

/** Query parameters for the notification feed endpoint. */
export interface NotifListParams {
  source?: NotifSource;
  severity?: NotifSeverity;
  read?: "read" | "unread";
  since?: string;
  until?: string;
  limit?: number;
  offset?: number;
}

/** SubNav tabs for the admin notification center pages. */
export const NOTIF_ADMIN_TABS = [
  { label: "Feed", href: "/admin/notifications/feed" },
  { label: "Channels", href: "/admin/notifications/channels" },
  { label: "Rules", href: "/admin/notifications/rules" },
];

/** All known notification sources for filter dropdowns. */
export const NOTIF_SOURCES: NotifSource[] = [
  "alert",
  "policy",
  "gitops",
  "diagnostic",
  "scan",
  "cluster",
  "audit",
  "limits",
  "velero",
  "certmanager",
  "external_secrets",
];

/** Source category labels for grouped UI rendering. The 11-entry source list
 * is no longer flat-flat-readable; categories let operators scan by domain.
 * Order within each category is roughly install-base frequency. */
export const NOTIF_SOURCE_CATEGORIES: ReadonlyArray<{
  label: string;
  sources: NotifSource[];
}> = [
  {
    label: "Infrastructure",
    sources: ["cluster", "limits", "scan"],
  },
  {
    label: "Policy",
    sources: ["policy", "diagnostic"],
  },
  {
    label: "Secrets",
    sources: ["certmanager", "external_secrets"],
  },
  {
    label: "Operations",
    sources: ["alert", "audit", "gitops", "velero"],
  },
];

/** All known notification severities for filter dropdowns. */
export const NOTIF_SEVERITIES: NotifSeverity[] = [
  "critical",
  "warning",
  "info",
];

/** Severity → CSS color variable mapping. */
export const NOTIF_SEVERITY_COLORS: Record<NotifSeverity, string> = {
  critical: "var(--danger)",
  warning: "var(--warning)",
  info: "var(--accent)",
};

/** Source → display label mapping. */
export const SOURCE_LABELS: Record<NotifSource, string> = {
  alert: "Alert",
  policy: "Policy",
  gitops: "GitOps",
  diagnostic: "Diagnostic",
  scan: "Security",
  cluster: "Cluster",
  audit: "Audit",
  limits: "Limits",
  velero: "Backup",
  certmanager: "Certificate",
  external_secrets: "External Secrets",
};
