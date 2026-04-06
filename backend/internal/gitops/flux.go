package gitops

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	fluxKustomizationGVR = schema.GroupVersionResource{
		Group:    "kustomize.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "kustomizations",
	}
	fluxHelmReleaseGVR = schema.GroupVersionResource{
		Group:    "helm.toolkit.fluxcd.io",
		Version:  "v2",
		Resource: "helmreleases",
	}
)

// ListFluxKustomizations lists all Flux Kustomization resources across namespaces
// and returns them as normalized GitOps applications.
func ListFluxKustomizations(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedApp, error) {
	list, err := dynClient.Resource(fluxKustomizationGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing flux kustomizations: %w", err)
	}

	apps := make([]NormalizedApp, 0, len(list.Items))
	for i := range list.Items {
		apps = append(apps, normalizeFluxKustomization(&list.Items[i]))
	}
	return apps, nil
}

// ListFluxHelmReleases lists all Flux HelmRelease resources across namespaces
// and returns them as normalized GitOps applications.
func ListFluxHelmReleases(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedApp, error) {
	list, err := dynClient.Resource(fluxHelmReleaseGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing flux helmreleases: %w", err)
	}

	apps := make([]NormalizedApp, 0, len(list.Items))
	for i := range list.Items {
		apps = append(apps, normalizeFluxHelmRelease(&list.Items[i]))
	}
	return apps, nil
}

// GetFluxAppDetail retrieves a single Flux application (Kustomization or HelmRelease)
// and returns full detail including managed resources and history.
func GetFluxAppDetail(ctx context.Context, dynClient dynamic.Interface, kind, namespace, name string) (*AppDetail, error) {
	var gvr schema.GroupVersionResource
	switch kind {
	case "Kustomization":
		gvr = fluxKustomizationGVR
	case "HelmRelease":
		gvr = fluxHelmReleaseGVR
	default:
		return nil, fmt.Errorf("unsupported flux kind: %s", kind)
	}

	obj, err := dynClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting flux %s %s/%s: %w", kind, namespace, name, err)
	}

	detail := &AppDetail{}

	switch kind {
	case "Kustomization":
		detail.App = normalizeFluxKustomization(obj)
		detail.Resources = extractFluxInventory(obj)
		// Flux Kustomizations have no CRD-native revision history.
	case "HelmRelease":
		detail.App = normalizeFluxHelmRelease(obj)
		detail.History = extractFluxHelmHistory(obj)
	}

	return detail, nil
}

func normalizeFluxKustomization(obj *unstructured.Unstructured) NormalizedApp {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	// Source reference
	sourceKind, _, _ := unstructured.NestedString(obj.Object, "spec", "sourceRef", "kind")
	sourceName, _, _ := unstructured.NestedString(obj.Object, "spec", "sourceRef", "name")
	path, _, _ := unstructured.NestedString(obj.Object, "spec", "path")

	// Destination namespace
	destNS, _, _ := unstructured.NestedString(obj.Object, "spec", "targetNamespace")
	if destNS == "" {
		destNS = namespace
	}

	// Suspended
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")

	// Status conditions
	conditions := extractConditions(obj)
	syncStatus, healthStatus, message := mapFluxConditions(conditions)

	if suspended {
		healthStatus = HealthSuspended
	}

	// Revision and last sync time
	currentRevision, _, _ := unstructured.NestedString(obj.Object, "status", "lastAppliedRevision")
	lastSyncTime := resolveLastSyncTime(obj, conditions)

	// Managed resource count from inventory
	managedCount := 0
	entries, found, _ := unstructured.NestedSlice(obj.Object, "status", "inventory", "entries")
	if found {
		managedCount = len(entries)
	}

	return NormalizedApp{
		ID:        fmt.Sprintf("flux-ks:%s:%s", namespace, name),
		Name:      name,
		Namespace: namespace,
		Tool:      ToolFluxCD,
		Kind:      "Kustomization",
		SyncStatus:   syncStatus,
		HealthStatus: healthStatus,
		Source: AppSource{
			RepoURL: fmt.Sprintf("%s/%s", sourceKind, sourceName),
			Path:    path,
		},
		CurrentRevision:      currentRevision,
		LastSyncTime:         lastSyncTime,
		Message:              message,
		DestinationNamespace: destNS,
		ManagedResourceCount: managedCount,
		Suspended:            suspended,
	}
}

func normalizeFluxHelmRelease(obj *unstructured.Unstructured) NormalizedApp {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	// Chart spec
	chartName, _, _ := unstructured.NestedString(obj.Object, "spec", "chart", "spec", "chart")
	chartVersion, _, _ := unstructured.NestedString(obj.Object, "spec", "chart", "spec", "version")
	sourceKind, _, _ := unstructured.NestedString(obj.Object, "spec", "chart", "spec", "sourceRef", "kind")
	sourceName, _, _ := unstructured.NestedString(obj.Object, "spec", "chart", "spec", "sourceRef", "name")

	// Destination namespace
	destNS, _, _ := unstructured.NestedString(obj.Object, "spec", "targetNamespace")
	if destNS == "" {
		destNS = namespace
	}

	// Suspended
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")

	// Status conditions
	conditions := extractConditions(obj)
	syncStatus, healthStatus, message := mapFluxConditions(conditions)

	if suspended {
		healthStatus = HealthSuspended
	}

	// Revision
	currentRevision, _, _ := unstructured.NestedString(obj.Object, "status", "lastAppliedRevision")
	lastSyncTime := resolveLastSyncTime(obj, conditions)

	return NormalizedApp{
		ID:        fmt.Sprintf("flux-hr:%s:%s", namespace, name),
		Name:      name,
		Namespace: namespace,
		Tool:      ToolFluxCD,
		Kind:      "HelmRelease",
		SyncStatus:   syncStatus,
		HealthStatus: healthStatus,
		Source: AppSource{
			RepoURL:      fmt.Sprintf("%s/%s", sourceKind, sourceName),
			ChartName:    chartName,
			ChartVersion: chartVersion,
		},
		CurrentRevision:      currentRevision,
		LastSyncTime:         lastSyncTime,
		Message:              message,
		DestinationNamespace: destNS,
		Suspended:            suspended,
	}
}

// mapFluxConditions maps Flux status conditions to normalized sync/health status
// and extracts the Ready condition message.
func mapFluxConditions(conditions []map[string]string) (SyncStatus, HealthStatus, string) {
	syncStatus := SyncUnknown
	healthStatus := HealthUnknown
	var message string

	var readyStatus, readyReason, readyMessage string
	var reconcilingTrue, stalledTrue, healthCheckFailedTrue bool

	for _, c := range conditions {
		condType := c["type"]
		condStatus := c["status"]

		switch condType {
		case "Ready":
			readyStatus = condStatus
			readyReason = c["reason"]
			readyMessage = c["message"]
		case "Reconciling":
			reconcilingTrue = condStatus == "True"
		case "Stalled":
			stalledTrue = condStatus == "True"
		case "HealthCheckFailed":
			healthCheckFailedTrue = condStatus == "True"
		}
	}

	message = readyMessage

	// Priority: Stalled > Reconciling > Ready
	switch {
	case stalledTrue:
		syncStatus = SyncStalled
		healthStatus = HealthDegraded
	case reconcilingTrue:
		syncStatus = SyncProgressing
		healthStatus = HealthProgressing
	case readyStatus == "True":
		syncStatus = SyncSynced
		healthStatus = HealthHealthy
	case readyStatus == "False":
		// Distinguish between transient failure and persistent out-of-sync
		if strings.Contains(readyReason, "Failed") ||
			strings.Contains(readyReason, "Error") ||
			readyReason == "ReconciliationFailed" ||
			readyReason == "ArtifactFailed" {
			syncStatus = SyncFailed
		} else {
			syncStatus = SyncOutOfSync
		}
		healthStatus = HealthDegraded
	}

	// HealthCheckFailed overrides health regardless of other conditions
	if healthCheckFailedTrue {
		healthStatus = HealthDegraded
	}

	return syncStatus, healthStatus, message
}

// extractConditions pulls status.conditions from an unstructured object
// into a slice of string maps for easier processing.
func extractConditions(obj *unstructured.Unstructured) []map[string]string {
	raw, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if !found {
		return nil
	}

	conditions := make([]map[string]string, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		c := make(map[string]string)
		for _, key := range []string{"type", "status", "reason", "message", "lastTransitionTime"} {
			if v, ok := m[key].(string); ok {
				c[key] = v
			}
		}
		conditions = append(conditions, c)
	}
	return conditions
}

// resolveLastSyncTime extracts lastHandledReconcileAt or falls back to the
// Ready condition's lastTransitionTime.
func resolveLastSyncTime(obj *unstructured.Unstructured, conditions []map[string]string) string {
	if t, found, _ := unstructured.NestedString(obj.Object, "status", "lastHandledReconcileAt"); found && t != "" {
		return t
	}
	for _, c := range conditions {
		if c["type"] == "Ready" {
			return c["lastTransitionTime"]
		}
	}
	return ""
}

// extractFluxInventory parses inventory entries from a Flux Kustomization.
// Each entry ID has the format "namespace_name_group_kind".
func extractFluxInventory(obj *unstructured.Unstructured) []ManagedResource {
	entries, found, _ := unstructured.NestedSlice(obj.Object, "status", "inventory", "entries")
	if !found {
		return nil
	}

	resources := make([]ManagedResource, 0, len(entries))
	for _, entry := range entries {
		eMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}

		id, ok := eMap["id"].(string)
		if !ok || id == "" {
			continue
		}

		// Format: namespace_name_group_kind
		parts := strings.SplitN(id, "_", 4)
		if len(parts) < 4 {
			continue
		}

		resources = append(resources, ManagedResource{
			Namespace: parts[0],
			Name:      parts[1],
			Group:     parts[2],
			Kind:      parts[3],
			Status:    "Synced", // Inventory entries are applied resources
		})
	}
	return resources
}

// extractFluxHelmHistory parses Helm release history entries from status.history.
func extractFluxHelmHistory(obj *unstructured.Unstructured) []RevisionEntry {
	history, found, _ := unstructured.NestedSlice(obj.Object, "status", "history")
	if !found {
		return nil
	}

	entries := make([]RevisionEntry, 0, len(history))
	for _, item := range history {
		hMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		revision, _, _ := unstructured.NestedString(hMap, "chartVersion")
		if revision == "" {
			// Fallback to digest
			revision, _, _ = unstructured.NestedString(hMap, "digest")
		}
		status, _, _ := unstructured.NestedString(hMap, "status")
		deployedAt, _, _ := unstructured.NestedString(hMap, "firstDeployed")

		entries = append(entries, RevisionEntry{
			Revision:   revision,
			Status:     status,
			DeployedAt: deployedAt,
		})
	}
	return entries
}
