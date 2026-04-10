package yaml

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	sigyaml "sigs.k8s.io/yaml"
)

// DiffDocument describes the diff between current and proposed state
// for a single YAML document.
type DiffDocument struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	IsNew     bool   `json:"isNew"`
	Current   string `json:"current"`  // Clean YAML of current state (empty if new)
	Proposed  string `json:"proposed"` // Clean YAML of proposed state after dry-run
	Error     string `json:"error,omitempty"`
}

// DiffResponse is the response envelope for a multi-document diff.
type DiffResponse struct {
	Documents []DiffDocument `json:"documents"`
}

// DiffDocuments performs a dry-run server-side apply for each document and
// returns the current vs proposed state as clean YAML strings. The frontend
// renders these in Monaco's diff editor.
func DiffDocuments(
	ctx context.Context,
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
	docs []*unstructured.Unstructured,
	logger *slog.Logger,
) *DiffResponse {
	resp := &DiffResponse{
		Documents: make([]DiffDocument, 0, len(docs)),
	}

	for i, obj := range docs {
		doc := diffOne(ctx, dynClient, mapper, obj, i, logger)
		resp.Documents = append(resp.Documents, doc)
	}

	return resp
}

// diffOne performs a dry-run apply for a single document and returns
// the current and proposed YAML.
func diffOne(
	ctx context.Context,
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
	obj *unstructured.Unstructured,
	index int,
	logger *slog.Logger,
) DiffDocument {
	doc := DiffDocument{
		Index:     index,
		Kind:      obj.GetKind(),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	// Resolve GVK → GVR
	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		doc.Error = fmt.Sprintf("unknown resource type %s: %v", gvk.String(), err)
		return doc
	}

	// Get the appropriate resource interface
	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ns := obj.GetNamespace()
		if ns == "" {
			// This matches kubectl's behavior: when metadata.namespace is
			// omitted, the resource lands in "default". A future improvement
			// could accept the frontend's selected namespace as a fallback
			// or reject resources without an explicit namespace.
			ns = "default"
		}
		doc.Namespace = ns
		dr = dynClient.Resource(mapping.Resource).Namespace(ns)
	} else {
		dr = dynClient.Resource(mapping.Resource)
	}

	// Get current state
	current, err := dr.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			doc.IsNew = true
		} else {
			doc.Error = fmt.Sprintf("failed to get current state: %v", err)
			return doc
		}
	}

	// Dry-run apply to get proposed state
	data, err := json.Marshal(obj.Object)
	if err != nil {
		doc.Error = fmt.Sprintf("marshaling object: %v", err)
		return doc
	}

	proposed, err := dr.Patch(ctx, obj.GetName(), types.ApplyPatchType, data,
		metav1.PatchOptions{
			FieldManager: FieldManager,
			DryRun:       []string{metav1.DryRunAll},
		})
	if err != nil {
		doc.Error = fmt.Sprintf("dry-run apply failed: %v", err)
		return doc
	}

	// Clean and serialize both to YAML
	if current != nil {
		cleaned := CleanForExport(current)
		yamlBytes, err := sigyaml.Marshal(cleaned.Object)
		if err != nil {
			doc.Error = fmt.Sprintf("serializing current state: %v", err)
			return doc
		}
		doc.Current = string(yamlBytes)
	}

	cleanedProposed := CleanForExport(proposed)
	yamlBytes, err := sigyaml.Marshal(cleanedProposed.Object)
	if err != nil {
		doc.Error = fmt.Sprintf("serializing proposed state: %v", err)
		return doc
	}
	doc.Proposed = string(yamlBytes)

	logger.Debug("yaml diff",
		"kind", doc.Kind,
		"name", doc.Name,
		"namespace", doc.Namespace,
		"isNew", doc.IsNew,
	)

	return doc
}
