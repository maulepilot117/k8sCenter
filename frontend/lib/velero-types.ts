/** VeleroStatus is the status response from the velero status API. */
export interface VeleroStatus {
  detected: boolean;
  namespace?: string;
  version?: string;
  bslCount: number;
  vslCount: number;
  lastChecked: string;
}

/** Backup represents a Velero backup. */
export interface Backup {
  name: string;
  namespace: string;
  phase: string;
  includedNamespaces?: string[];
  excludedNamespaces?: string[];
  storageLocation?: string;
  ttl?: string;
  snapshotVolumes: boolean;
  startTime?: string;
  completionTime?: string;
  expiration?: string;
  itemsBackedUp: number;
  totalItems: number;
  warnings: number;
  errors: number;
  scheduleName?: string;
  labels?: Record<string, string>;
}

/** Restore represents a Velero restore. */
export interface Restore {
  name: string;
  namespace: string;
  phase: string;
  backupName?: string;
  scheduleName?: string;
  includedNamespaces?: string[];
  namespaceMapping?: Record<string, string>;
  startTime?: string;
  completionTime?: string;
  itemsRestored: number;
  totalItems: number;
  warnings: number;
  errors: number;
  failureReason?: string;
}

/** Schedule represents a Velero backup schedule. */
export interface Schedule {
  name: string;
  namespace: string;
  phase: string;
  schedule: string;
  paused: boolean;
  includedNamespaces?: string[];
  storageLocation?: string;
  ttl?: string;
  lastBackup?: string;
  nextRunTime?: string;
  validationErrors?: string[];
}

/** BackupStorageLocation represents a Velero BSL. */
export interface BackupStorageLocation {
  name: string;
  namespace: string;
  provider: string;
  bucket: string;
  prefix?: string;
  phase: string;
  default: boolean;
  lastSyncedTime?: string;
  message?: string;
}

/** VolumeSnapshotLocation represents a Velero VSL. */
export interface VolumeSnapshotLocation {
  name: string;
  namespace: string;
  provider: string;
}

/** LocationsResponse is the response from the locations endpoint. */
export interface LocationsResponse {
  backupStorageLocations: BackupStorageLocation[];
  volumeSnapshotLocations: VolumeSnapshotLocation[];
}

/** Phase helper types for UI styling. */
export type PhaseCategory =
  | "success"
  | "warning"
  | "error"
  | "progress"
  | "unknown";

/** Get the category of a Velero phase for UI styling. */
export function getPhaseCategory(phase: string): PhaseCategory {
  const lowerPhase = phase.toLowerCase();

  // Failed states
  if (lowerPhase.includes("failed")) return "error";

  // Partial failure / warning states
  if (lowerPhase === "partiallyfailed") return "warning";

  // Success states
  if (["completed", "available", "enabled"].includes(lowerPhase)) {
    return "success";
  }

  // In-progress states
  if (
    [
      "inprogress",
      "new",
      "waitingforpluginoperations",
      "finalizing",
      "queued",
      "readytostart",
    ].includes(lowerPhase)
  ) {
    return "progress";
  }

  return "unknown";
}

/** Wizard input types for creating resources. */

export interface BackupInput {
  name: string;
  namespace: string;
  includedNamespaces?: string[];
  excludedNamespaces?: string[];
  storageLocation?: string;
  ttl?: string;
  snapshotVolumes?: boolean;
  labels?: Record<string, string>;
}

export interface RestoreInput {
  name: string;
  namespace: string;
  backupName?: string;
  scheduleName?: string;
  includedNamespaces?: string[];
  excludedNamespaces?: string[];
  namespaceMapping?: Record<string, string>;
  restorePVs?: boolean;
}

export interface ScheduleInput {
  name: string;
  namespace: string;
  schedule: string;
  paused?: boolean;
  includedNamespaces?: string[];
  excludedNamespaces?: string[];
  storageLocation?: string;
  ttl?: string;
  snapshotVolumes?: boolean;
}
