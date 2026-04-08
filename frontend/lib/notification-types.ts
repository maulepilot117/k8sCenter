export interface NormalizedProvider {
  name: string;
  namespace: string;
  type: string;
  channel: string;
  address: string;
  secretRef: string;
  suspend: boolean;
  status: string;
  message: string;
  createdAt: string;
}

export interface NormalizedAlert {
  name: string;
  namespace: string;
  providerRef: string;
  eventSeverity: string;
  eventSources: EventSourceRef[];
  inclusionList: string[];
  exclusionList: string[];
  suspend: boolean;
  status: string;
  message: string;
  createdAt: string;
}

export interface EventSourceRef {
  kind: string;
  name: string;
  namespace: string;
  matchLabels?: Record<string, string>;
}

export interface NormalizedReceiver {
  name: string;
  namespace: string;
  type: string;
  resources: EventSourceRef[];
  secretRef: string;
  suspend: boolean;
  webhookPath: string;
  status: string;
  message: string;
  createdAt: string;
}

export interface NotificationStatus {
  available: boolean;
  providerCount: number;
  alertCount: number;
  receiverCount: number;
}

/** Shared page size for all notification list views. */
export const PAGE_SIZE = 100;

/** Shared Tailwind class string for form inputs in notification modals. */
export const INPUT_CLASS =
  "w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand";

/** Flux CRD resource kinds used in alert event sources and receiver resource subscriptions. */
export const FLUX_RESOURCE_KINDS = [
  "Kustomization",
  "HelmRelease",
  "GitRepository",
  "HelmRepository",
  "HelmChart",
  "Bucket",
  "OCIRepository",
  "ImageRepository",
  "ImagePolicy",
  "ImageUpdateAutomation",
] as const;
