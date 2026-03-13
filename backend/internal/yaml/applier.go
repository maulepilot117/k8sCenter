package yaml

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/api/meta"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// FieldManager is the server-side apply field manager name for KubeCenter.
const FieldManager = "kubecenter"

// ApplyResult describes the outcome of applying a single YAML document.
type ApplyResult struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Action    string `json:"action"` // "created", "configured", "unchanged", "failed"
	Error     string `json:"error,omitempty"`
}

// ApplySummary provides aggregate counts for a multi-document apply.
type ApplySummary struct {
	Total      int `json:"total"`
	Created    int `json:"created"`
	Configured int `json:"configured"`
	Unchanged  int `json:"unchanged"`
	Failed     int `json:"failed"`
}

// ApplyResponse is the response envelope for a multi-document apply.
type ApplyResponse struct {
	Results []ApplyResult `json:"results"`
	Summary ApplySummary  `json:"summary"`
}

// ApplyDocuments applies a list of parsed Kubernetes objects via server-side
// apply. Each document is applied independently (best-effort). Results are
// returned per-document.
func ApplyDocuments(
	ctx context.Context,
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
	docs []*unstructured.Unstructured,
	force bool,
	logger *slog.Logger,
) *ApplyResponse {
	resp := &ApplyResponse{
		Results: make([]ApplyResult, 0, len(docs)),
	}

	for i, obj := range docs {
		result := applyOne(ctx, dynClient, mapper, obj, i, force, logger)
		resp.Results = append(resp.Results, result)
		switch result.Action {
		case "created":
			resp.Summary.Created++
		case "configured":
			resp.Summary.Configured++
		case "unchanged":
			resp.Summary.Unchanged++
		case "failed":
			resp.Summary.Failed++
		}
	}
	resp.Summary.Total = len(resp.Results)

	return resp
}

// applyOne applies a single unstructured object via server-side apply.
func applyOne(
	ctx context.Context,
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
	obj *unstructured.Unstructured,
	index int,
	force bool,
	logger *slog.Logger,
) ApplyResult {
	result := ApplyResult{
		Index:     index,
		Kind:      obj.GetKind(),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	// Resolve GVK → GVR
	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		result.Action = "failed"
		result.Error = fmt.Sprintf("unknown resource type %s: %v", gvk.String(), err)
		return result
	}

	// Get the appropriate resource interface (namespaced or cluster-scoped)
	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ns := obj.GetNamespace()
		if ns == "" {
			ns = "default"
		}
		result.Namespace = ns
		dr = dynClient.Resource(mapping.Resource).Namespace(ns)
	} else {
		dr = dynClient.Resource(mapping.Resource)
	}

	// Check if resource exists (for action detection)
	existing, getErr := dr.Get(ctx, obj.GetName(), metav1.GetOptions{})
	isNew := apierrors.IsNotFound(getErr)

	// Serialize to JSON for the patch payload
	data, err := json.Marshal(obj.Object)
	if err != nil {
		result.Action = "failed"
		result.Error = fmt.Sprintf("marshaling object: %v", err)
		return result
	}

	// Build patch options
	opts := metav1.PatchOptions{
		FieldManager: FieldManager,
	}
	if force {
		forceVal := true
		opts.Force = &forceVal
	}

	// Apply via SSA PATCH
	applied, err := dr.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, opts)
	if err != nil {
		result.Action = "failed"
		if apierrors.IsConflict(err) {
			result.Error = fmt.Sprintf("field ownership conflict: %v. Use force to override.", err)
		} else if apierrors.IsForbidden(err) {
			result.Error = fmt.Sprintf("permission denied: %v", err)
		} else if apierrors.IsInvalid(err) {
			result.Error = extractValidationMessage(err)
		} else {
			result.Error = err.Error()
		}
		return result
	}

	// Determine action: created, configured, or unchanged
	if isNew {
		result.Action = "created"
	} else if existing != nil && existing.GetResourceVersion() == applied.GetResourceVersion() {
		result.Action = "unchanged"
	} else {
		result.Action = "configured"
	}

	logger.Info("yaml apply",
		"action", result.Action,
		"kind", result.Kind,
		"name", result.Name,
		"namespace", result.Namespace,
	)

	return result
}

// extractValidationMessage extracts a user-friendly validation error message
// from a Kubernetes StatusError.
func extractValidationMessage(err error) string {
	if statusErr, ok := err.(*apierrors.StatusError); ok {
		return statusErr.Status().Message
	}
	return err.Error()
}
