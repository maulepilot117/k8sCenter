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
  | "audit";

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

/** All known notification sources for filter dropdowns. */
export const NOTIF_SOURCES: NotifSource[] = [
  "alert",
  "policy",
  "gitops",
  "diagnostic",
  "scan",
  "cluster",
  "audit",
];

/** All known notification severities for filter dropdowns. */
export const NOTIF_SEVERITIES: NotifSeverity[] = [
  "critical",
  "warning",
  "info",
];

/** Severity → CSS color variable mapping. */
export const SEVERITY_COLORS: Record<NotifSeverity, string> = {
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
};
