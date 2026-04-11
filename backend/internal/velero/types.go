// Package velero provides Velero backup/restore integration for k8sCenter.
package velero

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
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
	LastBackupPhase    string     `json:"lastBackupPhase,omitempty"`
	ValidationErrors   []string   `json:"validationErrors,omitempty"`
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
}

// Phase helper functions for UI badge coloring

// IsFailedPhase returns true for failed phases.
func IsFailedPhase(phase string) bool {
	switch phase {
	case "Failed", "FailedValidation":
		return true
	}
	return false
}

// IsWarningPhase returns true for partial failure phases.
func IsWarningPhase(phase string) bool {
	return phase == "PartiallyFailed"
}

// IsSuccessPhase returns true for success phases.
func IsSuccessPhase(phase string) bool {
	switch phase {
	case "Completed", "Available", "Enabled":
		return true
	}
	return false
}

// IsProgressPhase returns true for in-progress phases.
func IsProgressPhase(phase string) bool {
	switch phase {
	case "InProgress", "New", "WaitingForPluginOperations", "Finalizing",
		"Queued", "ReadyToStart", "FinalizingPartiallyFailed",
		"WaitingForPluginOperationsPartiallyFailed":
		return true
	}
	return false
}
