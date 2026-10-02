// Package velero provides Velero backup/restore integration for k8sCenter.
package velero

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// GVR constants for Velero CRDs
var (
	BackupGVR = schema.GroupVersionResource{
		Group: "velero.io", Version: "v1", Resource: "backups",
	}
	RestoreGVR = schema.GroupVersionResource{
		Group: "velero.io", Version: "v1", Resource: "restores",
	}
	ScheduleGVR = schema.GroupVersionResource{
		Group: "velero.io", Version: "v1", Resource: "schedules",
	}
	BackupStorageLocationGVR = schema.GroupVersionResource{
		Group: "velero.io", Version: "v1", Resource: "backupstoragelocations",
	}
	VolumeSnapshotLocationGVR = schema.GroupVersionResource{
		Group: "velero.io", Version: "v1", Resource: "volumesnapshotlocations",
	}
	DeleteBackupRequestGVR = schema.GroupVersionResource{
		Group: "velero.io", Version: "v1", Resource: "deletebackuprequests",
	}
	DownloadRequestGVR = schema.GroupVersionResource{
		Group: "velero.io", Version: "v1", Resource: "downloadrequests",
	}
)

// VeleroStatus is returned by GET /velero/status
type VeleroStatus struct {
	Detected    bool      `json:"detected"`
	Namespace   string    `json:"namespace,omitempty"`
	Version     string    `json:"version,omitempty"`
	BSLCount    int       `json:"bslCount"`
	VSLCount    int       `json:"vslCount"`
	LastChecked time.Time `json:"lastChecked"`
	// Reason explains a remote cluster's Detected false: discovery_missing
	// when Velero is not installed there, or why it could not be told
	// (unreachable, discovery_unavailable, ...). Absent on the local cluster.
	Reason string `json:"reason,omitempty"`
}

// Backup is the API response for a Velero backup.
// Phase is passed through from Velero's native phases.
type Backup struct {
	Name               string            `json:"name"`
	Namespace          string            `json:"namespace"`
	Phase              string            `json:"phase"`
	IncludedNamespaces []string          `json:"includedNamespaces"`
	ExcludedNamespaces []string          `json:"excludedNamespaces"`
	StorageLocation    string            `json:"storageLocation"`
	TTL                string            `json:"ttl"`
	StartTime          *time.Time        `json:"startTime,omitempty"`
	CompletionTime     *time.Time        `json:"completionTime,omitempty"`
	Expiration         *time.Time        `json:"expiration,omitempty"`
	ItemsBackedUp      int               `json:"itemsBackedUp"`
	TotalItems         int               `json:"totalItems"`
	Warnings           int               `json:"warnings"`
	Errors             int               `json:"errors"`
	ScheduleName       string            `json:"scheduleName,omitempty"`
	SnapshotVolumes    bool              `json:"snapshotVolumes"`
	Labels             map[string]string `json:"labels,omitempty"`

	// created is the object's creationTimestamp, which orders a schedule's
	// runs. Not serialized.
	created time.Time
}

// Restore is the API response for a Velero restore.
type Restore struct {
	Name               string            `json:"name"`
	Namespace          string            `json:"namespace"`
	Phase              string            `json:"phase"`
	BackupName         string            `json:"backupName"`
	ScheduleName       string            `json:"scheduleName,omitempty"`
	IncludedNamespaces []string          `json:"includedNamespaces"`
	NamespaceMapping   map[string]string `json:"namespaceMapping,omitempty"`
	StartTime          *time.Time        `json:"startTime,omitempty"`
	CompletionTime     *time.Time        `json:"completionTime,omitempty"`
	ItemsRestored      int               `json:"itemsRestored"`
	TotalItems         int               `json:"totalItems"`
	Warnings           int               `json:"warnings"`
	Errors             int               `json:"errors"`
	FailureReason      string            `json:"failureReason,omitempty"`
}

// Schedule is the API response for a Velero schedule.
type Schedule struct {
	Name               string     `json:"name"`
	Namespace          string     `json:"namespace"`
	Phase              string     `json:"phase"`
	Schedule           string     `json:"schedule"`
	Paused             bool       `json:"paused"`
	LastBackup         *time.Time `json:"lastBackup,omitempty"`
	NextRunTime        *time.Time `json:"nextRunTime,omitempty"`
	IncludedNamespaces []string   `json:"includedNamespaces"`
	TTL                string     `json:"ttl"`
	StorageLocation    string     `json:"storageLocation"`
	// LastBackupPhase is the phase of the schedule's newest Backup and
	// LastBackupOutcome what that phase means. Both are empty when the
	// schedule has no backup or the user cannot list backups. LastBackup
	// stays Velero's own status.lastBackup.
	LastBackupPhase   string        `json:"lastBackupPhase,omitempty"`
	LastBackupOutcome BackupOutcome `json:"lastBackupOutcome,omitempty"`
	ValidationErrors  []string      `json:"validationErrors,omitempty"`
}

// BackupStorageLocation is the API response for a BSL.
type BackupStorageLocation struct {
	Name           string     `json:"name"`
	Namespace      string     `json:"namespace"`
	Provider       string     `json:"provider"`
	Bucket         string     `json:"bucket"`
	Prefix         string     `json:"prefix,omitempty"`
	Phase          string     `json:"phase"`
	Default        bool       `json:"default"`
	LastSyncedTime *time.Time `json:"lastSyncedTime,omitempty"`
	Message        string     `json:"message,omitempty"`
}

// VolumeSnapshotLocation is the API response for a VSL.
type VolumeSnapshotLocation struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Provider  string `json:"provider"`
}

// LocationsResponse combines BSL and VSL lists.
type LocationsResponse struct {
	BackupStorageLocations  []BackupStorageLocation  `json:"backupStorageLocations"`
	VolumeSnapshotLocations []VolumeSnapshotLocation `json:"volumeSnapshotLocations"`
	// Coverage names a location list a remote cluster could not provide;
	// the other one is still served. Never set on the local cluster.
	Coverage []k8s.SourceCoverage `json:"coverage,omitempty"`
}

// IsProgressPhase reports whether phase is one Velero reports while a
// backup or restore is still running.
func IsProgressPhase(phase string) bool {
	switch phase {
	case "InProgress", "New", "WaitingForPluginOperations", "Finalizing",
		"Queued", "ReadyToStart", "FinalizingPartiallyFailed",
		"WaitingForPluginOperationsPartiallyFailed":
		return true
	}
	return false
}

// BackupOutcome is what a Backup's phase says about how it went.
type BackupOutcome string

const (
	BackupOutcomeSucceeded  BackupOutcome = "succeeded"
	BackupOutcomeFailed     BackupOutcome = "failed"
	BackupOutcomeInProgress BackupOutcome = "inProgress"
	// BackupOutcomeUnknown is a phase that is not a backup outcome: Deleting,
	// a phase this version does not know, or a phase of another kind.
	BackupOutcomeUnknown BackupOutcome = "unknown"
)

// BackupOutcomeOf classifies a Backup phase. Only Backup phases count:
// BackupStorageLocation and Schedule phases such as Available and Enabled
// are unknown, not successes. A partial failure is a failure, and the empty
// phase of a backup Velero has not picked up yet is in progress.
func BackupOutcomeOf(phase string) BackupOutcome {
	switch phase {
	case "Completed":
		return BackupOutcomeSucceeded
	case "PartiallyFailed", "Failed", "FailedValidation":
		return BackupOutcomeFailed
	}
	if phase == "" || IsProgressPhase(phase) {
		return BackupOutcomeInProgress
	}
	return BackupOutcomeUnknown
}
