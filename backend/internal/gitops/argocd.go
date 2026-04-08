package gitops

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// ArgoApplicationGVR is the GVR for Argo CD Application resources.
var ArgoApplicationGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "applications",
}

// ArgoApplicationSetGVR is the GVR for Argo CD ApplicationSet resources.
var ArgoApplicationSetGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "applicationsets",
}

// ListArgoApplications fetches all Argo CD Applications across namespaces
// and returns them as normalized app objects.
func ListArgoApplications(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedApp, error) {
	list, err := dynClient.Resource(ArgoApplicationGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing argo cd applications: %w", err)
	}

	apps := make([]NormalizedApp, 0, len(list.Items))
	for i := range list.Items {
		apps = append(apps, NormalizeArgoApp(&list.Items[i]))
	}

	return apps, nil
}

// GetArgoAppDetail fetches a single Argo CD Application and returns its
// full detail including managed resources and revision history.
func GetArgoAppDetail(ctx context.Context, dynClient dynamic.Interface, namespace, name string) (*AppDetail, error) {
	obj, err := dynClient.Resource(ArgoApplicationGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting argo cd application %s/%s: %w", namespace, name, err)
	}

	app := NormalizeArgoApp(obj)
	resources := extractArgoResources(obj)
	history := extractArgoHistory(obj)

	return &AppDetail{
		App:       app,
		Resources: resources,
		History:   history,
	}, nil
}

func NormalizeArgoApp(obj *unstructured.Unstructured) NormalizedApp {
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

// SyncArgoApp triggers a sync operation on an Argo CD Application.
func SyncArgoApp(ctx context.Context, dynClient dynamic.Interface, ns, name, username string) (*unstructured.Unstructured, error) {
	obj, err := dynClient.Resource(ArgoApplicationGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting argo cd application %s/%s: %w", ns, name, err)
	}

	phase, _, _ := unstructured.NestedString(obj.Object, "status", "operationState", "phase")
	if phase == "Running" {
		return nil, fmt.Errorf("sync already in progress for %s/%s", ns, name)
	}

	patch := map[string]interface{}{
		"operation": map[string]interface{}{
			"initiatedBy": map[string]interface{}{
				"username":  username,
				"automated": false,
			},
			"sync": map[string]interface{}{
				"syncStrategy": map[string]interface{}{
					"hook": map[string]interface{}{},
				},
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshaling sync patch: %w", err)
	}

	result, err := dynClient.Resource(ArgoApplicationGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return nil, fmt.Errorf("patching argo cd application %s/%s for sync: %w", ns, name, err)
	}

	return result, nil
}

// SuspendArgoApp disables automated sync on an Argo CD Application,
// preserving the previous sync policy in an annotation for later restore.
func SuspendArgoApp(ctx context.Context, dynClient dynamic.Interface, ns, name string) (*unstructured.Unstructured, error) {
	obj, err := dynClient.Resource(ArgoApplicationGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting argo cd application %s/%s: %w", ns, name, err)
	}

	automated, found, _ := unstructured.NestedMap(obj.Object, "spec", "syncPolicy", "automated")
	if !found || automated == nil {
		// Already has no auto-sync, nothing to suspend.
		return obj, nil
	}

	policyJSON, err := json.Marshal(automated)
	if err != nil {
		return nil, fmt.Errorf("marshaling automated sync policy: %w", err)
	}

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"kubecenter.io/pre-suspend-sync-policy": string(policyJSON),
			},
		},
		"spec": map[string]interface{}{
			"syncPolicy": map[string]interface{}{
				"automated": nil,
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshaling suspend patch: %w", err)
	}

	result, err := dynClient.Resource(ArgoApplicationGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return nil, fmt.Errorf("patching argo cd application %s/%s for suspend: %w", ns, name, err)
	}

	return result, nil
}

// ResumeArgoApp re-enables automated sync on an Argo CD Application,
// restoring the policy saved by SuspendArgoApp or using sensible defaults.
func ResumeArgoApp(ctx context.Context, dynClient dynamic.Interface, ns, name string) (*unstructured.Unstructured, error) {
	obj, err := dynClient.Resource(ArgoApplicationGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting argo cd application %s/%s: %w", ns, name, err)
	}

	annotations := obj.GetAnnotations()
	var automatedPolicy map[string]interface{}

	if saved, ok := annotations["kubecenter.io/pre-suspend-sync-policy"]; ok {
		if err := json.Unmarshal([]byte(saved), &automatedPolicy); err != nil {
			return nil, fmt.Errorf("unmarshaling saved sync policy: %w", err)
		}
	} else {
		automatedPolicy = map[string]interface{}{
			"prune":    true,
			"selfHeal": true,
		}
	}

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"kubecenter.io/pre-suspend-sync-policy": nil,
			},
		},
		"spec": map[string]interface{}{
			"syncPolicy": map[string]interface{}{
				"automated": automatedPolicy,
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshaling resume patch: %w", err)
	}

	result, err := dynClient.Resource(ArgoApplicationGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return nil, fmt.Errorf("patching argo cd application %s/%s for resume: %w", ns, name, err)
	}

	return result, nil
}

// ListArgoAppSets fetches all Argo CD ApplicationSets across namespaces
// and returns them as normalized objects.
func ListArgoAppSets(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedAppSet, error) {
	list, err := dynClient.Resource(ArgoApplicationSetGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing argo cd applicationsets: %w", err)
	}

	appSets := make([]NormalizedAppSet, 0, len(list.Items))
	for i := range list.Items {
		appSets = append(appSets, NormalizeArgoAppSet(&list.Items[i]))
	}

	return appSets, nil
}

// NormalizeArgoAppSet extracts a NormalizedAppSet from an unstructured ApplicationSet.
func NormalizeArgoAppSet(obj *unstructured.Unstructured) NormalizedAppSet {
	name := obj.GetName()
	ns := obj.GetNamespace()

	// Generators
	rawGenerators, _, _ := unstructured.NestedSlice(obj.Object, "spec", "generators")
	generatorTypes := make([]string, 0, len(rawGenerators))
	for _, raw := range rawGenerators {
		genMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		generatorTypes = append(generatorTypes, detectGeneratorType(genMap))
	}

	// Template source
	repoURL, _, _ := unstructured.NestedString(obj.Object, "spec", "template", "spec", "source", "repoURL")
	path, _, _ := unstructured.NestedString(obj.Object, "spec", "template", "spec", "source", "path")
	targetRevision, _, _ := unstructured.NestedString(obj.Object, "spec", "template", "spec", "source", "targetRevision")

	// Template destination
	destServer, _, _ := unstructured.NestedString(obj.Object, "spec", "template", "spec", "destination", "server")
	destNS, _, _ := unstructured.NestedString(obj.Object, "spec", "template", "spec", "destination", "namespace")
	var templateDest string
	if destServer != "" || destNS != "" {
		templateDest = destServer + "/" + destNS
	}

	// Status from conditions
	status := "Healthy"
	statusMessage := ""
	conditions, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if found {
		for _, raw := range conditions {
			condMap, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			condType, _, _ := unstructured.NestedString(condMap, "type")
			condStatus, _, _ := unstructured.NestedString(condMap, "status")

			if condType == "ErrorOccurred" && condStatus == "True" {
				status = "Error"
				statusMessage, _, _ = unstructured.NestedString(condMap, "message")
				break
			}
			if condType == "ResourcesUpToDate" && condStatus == "False" {
				status = "Progressing"
			}
		}
	}

	// PreserveOnDeletion
	preserveOnDeletion, _, _ := unstructured.NestedBool(obj.Object, "spec", "syncPolicy", "preserveResourcesOnDeletion")

	// CreatedAt
	createdAt := obj.GetCreationTimestamp().Format("2006-01-02T15:04:05Z")

	return NormalizedAppSet{
		ID:        fmt.Sprintf("argo-as:%s:%s", ns, name),
		Name:      name,
		Namespace: ns,
		Tool:      ToolArgoCD,
		GeneratorTypes: generatorTypes,
		TemplateSource: AppSource{
			RepoURL:        repoURL,
			Path:           path,
			TargetRevision: targetRevision,
		},
		TemplateDestination: templateDest,
		Status:              status,
		StatusMessage:       statusMessage,
		PreserveOnDeletion:  preserveOnDeletion,
		CreatedAt:           createdAt,
	}
}

// GetArgoAppSetDetail fetches a single Argo CD ApplicationSet and returns its full detail.
func GetArgoAppSetDetail(ctx context.Context, dynClient dynamic.Interface, namespace, name string) (*AppSetDetail, error) {
	obj, err := dynClient.Resource(ArgoApplicationSetGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting argo cd applicationset %s/%s: %w", namespace, name, err)
	}

	appSet := NormalizeArgoAppSet(obj)

	// Extract raw generators
	rawGenerators, _, _ := unstructured.NestedSlice(obj.Object, "spec", "generators")
	generators := make([]map[string]any, 0, len(rawGenerators))
	for _, raw := range rawGenerators {
		genMap, ok := raw.(map[string]interface{})
		if ok {
			generators = append(generators, genMap)
		}
	}

	// Extract conditions
	rawConditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	conditions := make([]AppSetCondition, 0, len(rawConditions))
	for _, raw := range rawConditions {
		condMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(condMap, "type")
		condStatus, _, _ := unstructured.NestedString(condMap, "status")
		condMessage, _, _ := unstructured.NestedString(condMap, "message")
		condReason, _, _ := unstructured.NestedString(condMap, "reason")
		conditions = append(conditions, AppSetCondition{
			Type:    condType,
			Status:  condStatus,
			Message: condMessage,
			Reason:  condReason,
		})
	}

	return &AppSetDetail{
		AppSet:     appSet,
		Generators: generators,
		Conditions: conditions,
	}, nil
}

// RefreshArgoAppSet patches an annotation to trigger an ApplicationSet refresh.
func RefreshArgoAppSet(ctx context.Context, dynClient dynamic.Interface, ns, name string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"argocd.argoproj.io/application-set-refresh": "true",
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling appset refresh patch: %w", err)
	}

	_, err = dynClient.Resource(ArgoApplicationSetGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patching argo cd applicationset %s/%s for refresh: %w", ns, name, err)
	}

	return nil
}

// DeleteArgoAppSet deletes an Argo CD ApplicationSet.
func DeleteArgoAppSet(ctx context.Context, dynClient dynamic.Interface, ns, name string) error {
	err := dynClient.Resource(ArgoApplicationSetGVR).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("deleting argo cd applicationset %s/%s: %w", ns, name, err)
	}
	return nil
}

// detectGeneratorType identifies the generator type from an ApplicationSet generator map.
func detectGeneratorType(gen map[string]interface{}) string {
	knownTypes := []string{"list", "git", "clusters", "matrix", "merge", "pullRequest", "scmProvider", "clusterDecisionResource", "plugin"}
	for _, t := range knownTypes {
		if _, ok := gen[t]; ok {
			return t
		}
	}
	return "unknown"
}

// RollbackArgoApp triggers a sync to a specific historical revision.
// Auto-sync must be disabled first, and the revision must exist in history.
func RollbackArgoApp(ctx context.Context, dynClient dynamic.Interface, ns, name, revision, username string) (*unstructured.Unstructured, error) {
	obj, err := dynClient.Resource(ArgoApplicationGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting argo cd application %s/%s: %w", ns, name, err)
	}

	automated, found, _ := unstructured.NestedMap(obj.Object, "spec", "syncPolicy", "automated")
	if found && automated != nil {
		return nil, fmt.Errorf("cannot rollback: auto-sync is enabled for %s/%s, suspend first", ns, name)
	}

	rawHistory, _, _ := unstructured.NestedSlice(obj.Object, "status", "history")
	revisionFound := false
	for _, entry := range rawHistory {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		rev, _, _ := unstructured.NestedString(entryMap, "revision")
		if rev == revision {
			revisionFound = true
			break
		}
	}
	if !revisionFound {
		return nil, fmt.Errorf("revision %q not found in history for %s/%s", revision, ns, name)
	}

	patch := map[string]interface{}{
		"operation": map[string]interface{}{
			"initiatedBy": map[string]interface{}{
				"username":  username,
				"automated": false,
			},
			"sync": map[string]interface{}{
				"revision": revision,
				"syncStrategy": map[string]interface{}{
					"hook": map[string]interface{}{},
				},
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshaling rollback patch: %w", err)
	}

	result, err := dynClient.Resource(ArgoApplicationGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return nil, fmt.Errorf("patching argo cd application %s/%s for rollback: %w", ns, name, err)
	}

	return result, nil
}
