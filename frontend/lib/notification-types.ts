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
