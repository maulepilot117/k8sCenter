package velero

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// unstructuredFromFuzz decodes fuzz bytes into an *unstructured.Unstructured.
// Inputs that don't decode to a JSON/YAML object are skipped — the seed corpus
// carries the structural diversity and the mutator explores around it.
func unstructuredFromFuzz(data []byte) (*unstructured.Unstructured, bool) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return &unstructured.Unstructured{Object: m}, true
}

// FuzzVeleroParsers asserts every Velero CRD parser is crash-safe on
// arbitrary/adversarial unstructured input. Oracle: no panic; zero-values fine.
func FuzzVeleroParsers(f *testing.F) {
	// --- realistic valid objects ---

	// Backup with storageLocation, ttl, phase
	f.Add([]byte(`{
		"apiVersion": "velero.io/v1",
		"kind": "Backup",
		"metadata": {"name": "daily-backup", "namespace": "velero"},
		"spec": {
			"storageLocation": "default",
			"ttl": "720h0m0s",
			"includedNamespaces": ["default", "kube-system"],
			"excludedNamespaces": [],
			"snapshotVolumes": true
		},
		"status": {
			"phase": "Completed",
			"startTimestamp": "2026-06-01T00:00:00Z",
			"completionTimestamp": "2026-06-01T00:05:00Z",
			"expiration": "2026-07-01T00:00:00Z",
			"progress": {"itemsBackedUp": 42, "totalItems": 42},
			"warnings": 0,
			"errors": 0
		}
	}`))

	// Restore with backupName and namespaceMapping
	f.Add([]byte(`{
		"apiVersion": "velero.io/v1",
		"kind": "Restore",
		"metadata": {"name": "restore-1", "namespace": "velero"},
		"spec": {
			"backupName": "daily-backup",
			"scheduleName": "daily-schedule",
			"includedNamespaces": ["default"],
			"namespaceMapping": {"default": "restored-ns"}
		},
		"status": {
			"phase": "Completed",
			"startTimestamp": "2026-06-02T00:00:00Z",
			"completionTimestamp": "2026-06-02T00:03:00Z",
			"progress": {"itemsRestored": 10, "totalItems": 10},
			"warnings": 0,
			"errors": 0
		}
	}`))

	// Schedule with cron expression and template
	f.Add([]byte(`{
		"apiVersion": "velero.io/v1",
		"kind": "Schedule",
		"metadata": {"name": "daily-schedule", "namespace": "velero"},
		"spec": {
			"schedule": "0 2 * * *",
			"paused": false,
			"template": {
				"includedNamespaces": ["default"],
				"ttl": "720h0m0s",
				"storageLocation": "default"
			}
		},
		"status": {
			"phase": "Enabled",
			"lastBackup": "2026-06-26T02:00:00Z"
		}
	}`))

	// BackupStorageLocation with provider and objectStorage
	f.Add([]byte(`{
		"apiVersion": "velero.io/v1",
		"kind": "BackupStorageLocation",
		"metadata": {"name": "default", "namespace": "velero"},
		"spec": {
			"provider": "aws",
			"default": true,
			"objectStorage": {
				"bucket": "my-velero-backups",
				"prefix": "cluster1"
			}
		},
		"status": {
			"phase": "Available",
			"lastSyncedTime": "2026-06-27T01:00:00Z",
			"message": ""
		}
	}`))

	// VolumeSnapshotLocation
	f.Add([]byte(`{
		"apiVersion": "velero.io/v1",
		"kind": "VolumeSnapshotLocation",
		"metadata": {"name": "default", "namespace": "velero"},
		"spec": {
			"provider": "aws"
		}
	}`))

	// --- malformed / adversarial seeds ---

	// Empty object
	f.Add([]byte(`{}`))

	// metadata is a string instead of an object
	f.Add([]byte(`{"metadata":"oops"}`))

	// spec is an array and status is a scalar
	f.Add([]byte(`{"spec":[],"status":"x"}`))

	// namespaceMapping and objectStorage are wrong types
	f.Add([]byte(`{"spec":{"namespaceMapping":"notamap","objectStorage":"x"}}`))

	// validationErrors is an object instead of a slice
	f.Add([]byte(`{"status":{"validationErrors":{}}}`))

	// deeply nested wrong types
	f.Add([]byte(`{"spec":{"template":{"includedNamespaces":42,"ttl":true}}}`))

	// progress fields are strings not ints
	f.Add([]byte(`{"status":{"progress":{"itemsBackedUp":"lots","totalItems":"many"}}}`))

	// labels is a slice instead of map
	f.Add([]byte(`{"metadata":{"labels":["velero.io/schedule-name","bad"]}}`))

	// timestamps are malformed
	f.Add([]byte(`{"status":{"startTimestamp":"not-a-time","expiration":12345}}`))

	// null values scattered throughout
	f.Add([]byte(`{"spec":null,"status":null,"metadata":null}`))

	// objectStorage bucket is an int
	f.Add([]byte(`{"spec":{"objectStorage":{"bucket":999,"prefix":false}}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		u, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}
		_ = parseBackup(u)
		_ = parseRestore(u)
		_ = parseSchedule(u)
		_ = parseBSL(u)
		_ = parseVSL(u)
	})
}
