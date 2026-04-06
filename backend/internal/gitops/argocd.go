package gitops

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var argoApplicationGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "applications",
}

// ListArgoApplications fetches all Argo CD Applications across namespaces
// and returns them as normalized app objects.
func ListArgoApplications(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedApp, error) {
	list, err := dynClient.Resource(argoApplicationGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing argo cd applications: %w", err)
	}

	apps := make([]NormalizedApp, 0, len(list.Items))
	for i := range list.Items {
		apps = append(apps, normalizeArgoApp(&list.Items[i]))
	}

	return apps, nil
}

// GetArgoAppDetail fetches a single Argo CD Application and returns its
// full detail including managed resources and revision history.
func GetArgoAppDetail(ctx context.Context, dynClient dynamic.Interface, namespace, name string) (*AppDetail, error) {
	obj, err := dynClient.Resource(argoApplicationGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting argo cd application %s/%s: %w", namespace, name, err)
	}

	app := normalizeArgoApp(obj)
	resources := extractArgoResources(obj)
	history := extractArgoHistory(obj)

	return &AppDetail{
		App:       app,
		Resources: resources,
		History:   history,
	}, nil
}

func normalizeArgoApp(obj *unstructured.Unstructured) NormalizedApp {
	name := obj.GetName()
	ns := obj.GetNamespace()

	// Source
	repoURL, _, _ := unstructured.NestedString(obj.Object, "spec", "source", "repoURL")
	path, _, _ := unstructured.NestedString(obj.Object, "spec", "source", "path")
	targetRevision, _, _ := unstructured.NestedString(obj.Object, "spec", "source", "targetRevision")

	// Destination
	destServer, _, _ := unstructured.NestedString(obj.Object, "spec", "destination", "server")
	destNS, _, _ := unstructured.NestedString(obj.Object, "spec", "destination", "namespace")

	// Status - sync
	syncStatusRaw, _, _ := unstructured.NestedString(obj.Object, "status", "sync", "status")
	syncStatus := mapArgoSyncStatus(syncStatusRaw)

	// Status - health
	healthStatusRaw, _, _ := unstructured.NestedString(obj.Object, "status", "health", "status")
	healthStatus := mapArgoHealthStatus(healthStatusRaw)

	// Status - message (operationState.message, fallback to first condition)
	message, _, _ := unstructured.NestedString(obj.Object, "status", "operationState", "message")
	if message == "" {
		conditions, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if found && len(conditions) > 0 {
			if condMap, ok := conditions[0].(map[string]interface{}); ok {
				message, _, _ = unstructured.NestedString(condMap, "message")
			}
		}
	}

	// Managed resource count
	resources, _, _ := unstructured.NestedSlice(obj.Object, "status", "resources")
	managedCount := len(resources)

	// Last sync time
	lastSyncTime, _, _ := unstructured.NestedString(obj.Object, "status", "reconciledAt")

	// Current revision
	currentRevision, _, _ := unstructured.NestedString(obj.Object, "status", "sync", "revision")

	// Suspended: health status == Suspended
	suspended := healthStatusRaw == "Suspended"

	return NormalizedApp{
		ID:        fmt.Sprintf("argo:%s:%s", ns, name),
		Name:      name,
		Namespace: ns,
		Tool:      ToolArgoCD,
		Kind:      "Application",
		SyncStatus: syncStatus,
		HealthStatus: healthStatus,
		Source: AppSource{
			RepoURL:        repoURL,
			Path:           path,
			TargetRevision: targetRevision,
		},
		CurrentRevision:      currentRevision,
		LastSyncTime:         lastSyncTime,
		Message:              message,
		DestinationCluster:   destServer,
		DestinationNamespace: destNS,
		ManagedResourceCount: managedCount,
		Suspended:            suspended,
	}
}

// mapArgoSyncStatus maps the raw Argo CD sync status string to a normalized SyncStatus.
func mapArgoSyncStatus(raw string) SyncStatus {
	switch raw {
	case "Synced":
		return SyncSynced
	case "OutOfSync":
		return SyncOutOfSync
	case "Unknown":
		return SyncUnknown
	default:
		return SyncUnknown
	}
}

// mapArgoHealthStatus maps the raw Argo CD health status string to a normalized HealthStatus.
func mapArgoHealthStatus(raw string) HealthStatus {
	switch raw {
	case "Healthy":
		return HealthHealthy
	case "Degraded":
		return HealthDegraded
	case "Progressing":
		return HealthProgressing
	case "Suspended":
		return HealthSuspended
	case "Missing":
		return HealthDegraded
	default:
		return HealthUnknown
	}
}

func extractArgoResources(obj *unstructured.Unstructured) []ManagedResource {
	rawResources, found, _ := unstructured.NestedSlice(obj.Object, "status", "resources")
	if !found {
		return nil
	}

	resources := make([]ManagedResource, 0, len(rawResources))
	for _, raw := range rawResources {
		resMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		group, _, _ := unstructured.NestedString(resMap, "group")
		kind, _, _ := unstructured.NestedString(resMap, "kind")
		namespace, _, _ := unstructured.NestedString(resMap, "namespace")
		name, _, _ := unstructured.NestedString(resMap, "name")
		status, _, _ := unstructured.NestedString(resMap, "status")
		health, _, _ := unstructured.NestedString(resMap, "health", "status")

		resources = append(resources, ManagedResource{
			Group:     group,
			Kind:      kind,
			Namespace: namespace,
			Name:      name,
			Status:    status,
			Health:    health,
		})
	}

	return resources
}

func extractArgoHistory(obj *unstructured.Unstructured) []RevisionEntry {
	rawHistory, found, _ := unstructured.NestedSlice(obj.Object, "status", "history")
	if !found {
		return nil
	}

	history := make([]RevisionEntry, 0, len(rawHistory))
	for _, raw := range rawHistory {
		entryMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		revision, _, _ := unstructured.NestedString(entryMap, "revision")
		deployedAt, _, _ := unstructured.NestedString(entryMap, "deployedAt")

		history = append(history, RevisionEntry{
			Revision:   revision,
			DeployedAt: deployedAt,
		})
	}

	return history
}
