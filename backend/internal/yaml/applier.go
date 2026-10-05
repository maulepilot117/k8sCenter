package yaml

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/changes"
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
//
// Tracking is set only for a tracked apply (?trackedOperationId=). It is a
// pointer with omitempty so an untracked response serializes to exactly
// {"results","summary"}, byte-identical to the format every legacy client
// (web, mobile, wizards) parses. tracked_apply_test.go enforces this.
type ApplyResponse struct {
	Results  []ApplyResult          `json:"results"`
	Summary  ApplySummary           `json:"summary"`
	Tracking *changes.ApplyTracking `json:"tracking,omitempty"`
}

// add appends one result and counts it in the summary.
func (r *ApplyResponse) add(result ApplyResult) {
	r.Results = append(r.Results, result)
	switch result.Action {
	case "created":
		r.Summary.Created++
	case "configured":
		r.Summary.Configured++
	case "unchanged":
		r.Summary.Unchanged++
	case "failed":
		r.Summary.Failed++
	}
}

// ApplyObservation is one document's outcome with the detail a change
// receipt records and ApplyResult deliberately keeps off the legacy wire.
type ApplyObservation struct {
	// Result is exactly the ApplyResult reported for the document.
	Result ApplyResult
	// Mapping is the resolved REST mapping; nil when GVK resolution failed.
	Mapping *meta.RESTMapping
	// UID identifies the object the outcome is about: the applied object's UID
	// on success; on a failed PATCH, the UID the pre-PATCH GET saw, so an
	// indeterminate outcome can be verified against the object that existed
	// (empty when there was none, or when the GET itself failed).
	UID types.UID
	// ErrorClass is changes.ClassifyAPIError of the SSA PATCH's error, set
	// only when that PATCH failed. A failure before the PATCH (mapping, the
	// pre-PATCH GET, marshaling) mutated nothing and is left unclassified.
	ErrorClass string

	// notSent marks a document abandoned because ctx was done before its PATCH
	// was issued. Only the observed loop acts on it.
	notSent bool
}

// ApplyObserver is called once per attempted document, in document order,
// before the next document is attempted. Returning an error STOPS the apply:
// no further document is sent to the cluster, and each remaining document is
// reported as failed with changes.NotAppliedError rather than omitted, so
// summary.total still equals len(docs) and summary.failed > 0 for legacy
// clients that compute success from it (plan D5).
//
// An observed apply also stops when ctx is done (the client hung up, or the
// BFF's 30s proxy cap cut the request). ctx is checked before each document
// and again immediately before each PATCH, and a document abandoned at either
// point is never observed: it and every later document are reported failed
// with changes.NotAttemptedError, which is the truth. Only a PATCH that was
// actually issued and then failed on the context or the transport is
// observed, and classified indeterminate. (A cancellation landing between the
// final check and the PATCH call is classified indeterminate too: the safe
// side, since it cannot be told apart from one that reached the server.)
type ApplyObserver func(ctx context.Context, obs ApplyObservation) error

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
	return ApplyDocumentsObserved(ctx, dynClient, mapper, docs, force, logger, nil)
}

// ApplyDocumentsObserved is ApplyDocuments plus a per-document observer. A
// nil observer observes nothing and does not stop early, which is exactly
// ApplyDocuments. See ApplyObserver for the stop contract.
func ApplyDocumentsObserved(
	ctx context.Context,
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
	docs []*unstructured.Unstructured,
	force bool,
	logger *slog.Logger,
	observe ApplyObserver,
) *ApplyResponse {
	resp := &ApplyResponse{
		Results: make([]ApplyResult, 0, len(docs)),
	}
	observed := observe != nil

	// stopText is the error every document from the stop point on carries;
	// empty while the apply is running.
	stopText := ""
	for i, obj := range docs {
		if stopText == "" && observed && ctx.Err() != nil {
			logger.Info("yaml apply stopped: request context done",
				"index", i, "notAttempted", len(docs)-i, "error", ctx.Err())
			stopText = changes.NotAttemptedError
		}
		if stopText != "" {
			resp.add(notAppliedResult(i, obj, stopText))
			continue
		}
		obs := applyOne(ctx, dynClient, mapper, obj, i, force, observed, logger)
		if observed && obs.notSent {
			logger.Info("yaml apply stopped: request context done before PATCH",
				"index", i, "notAttempted", len(docs)-i, "error", ctx.Err())
			stopText = changes.NotAttemptedError
			resp.add(notAppliedResult(i, obj, stopText))
			continue
		}
		resp.add(obs.Result)
		if !observed {
			continue
		}
		if err := observe(ctx, obs); err != nil {
			logger.Warn("yaml apply stopped: observer failed",
				"index", i, "notAttempted", len(docs)-i-1, "error", err)
			stopText = changes.NotAppliedError
		}
	}
	resp.Summary.Total = len(resp.Results)

	return resp
}

// notAppliedResult reports a document the apply never sent to the cluster.
func notAppliedResult(index int, obj *unstructured.Unstructured, text string) ApplyResult {
	return ApplyResult{
		Index:     index,
		Kind:      obj.GetKind(),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Action:    "failed",
		Error:     text,
	}
}

// restMappingRetryBackoff is the wait before RESTMapping retry attempt+1:
// 500ms, then 1s. A variable so tests can drop the wait.
var restMappingRetryBackoff = func(attempt int) time.Duration {
	return time.Duration(500*(1<<attempt)) * time.Millisecond
}

// applyOne applies a single unstructured object via server-side apply and
// reports its result together with the mapping, UID and PATCH error class.
//
// abandonOnCancel (set for observed applies) makes it re-check ctx
// immediately before the PATCH and, when ctx is done, return without issuing
// it, marked notSent. Unset, the PATCH is issued exactly as it always was.
func applyOne(
	ctx context.Context,
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
	obj *unstructured.Unstructured,
	index int,
	force bool,
	abandonOnCancel bool,
	logger *slog.Logger,
) ApplyObservation {
	obs := ApplyObservation{Result: ApplyResult{
		Index:     index,
		Kind:      obj.GetKind(),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}}
	result := &obs.Result

	// Resolve GVK → GVR, with retry for newly-registered CRDs (e.g. Gatekeeper
	// ConstraintTemplates that create CRDs on apply). The DeferredDiscoveryRESTMapper
	// invalidates its cache on miss, so subsequent attempts will re-discover.
	gvk := obj.GroupVersionKind()
	var mapping *meta.RESTMapping
	for attempt := range 3 {
		var mapErr error
		mapping, mapErr = mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if mapErr == nil {
			break
		}
		if attempt == 2 {
			result.Action = "failed"
			result.Error = fmt.Sprintf("unknown resource type %s: %v", gvk.String(), mapErr)
			return obs
		}
		logger.Info("waiting for CRD discovery", "gvk", gvk.String(), "attempt", attempt+1)
		select {
		case <-ctx.Done():
			result.Action = "failed"
			result.Error = fmt.Sprintf("context cancelled waiting for CRD %s", gvk.String())
			obs.notSent = true
			return obs
		case <-time.After(restMappingRetryBackoff(attempt)):
		}
	}
	obs.Mapping = mapping

	// Get the appropriate resource interface (namespaced or cluster-scoped)
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
		result.Namespace = ns
		dr = dynClient.Resource(mapping.Resource).Namespace(ns)
	} else {
		dr = dynClient.Resource(mapping.Resource)
	}

	// Check if resource exists (for action detection)
	existing, getErr := dr.Get(ctx, obj.GetName(), metav1.GetOptions{})
	isNew := apierrors.IsNotFound(getErr)
	var priorUID types.UID
	if getErr == nil && existing != nil {
		priorUID = existing.GetUID()
	}

	// Serialize to JSON for the patch payload
	data, err := json.Marshal(obj.Object)
	if err != nil {
		result.Action = "failed"
		result.Error = fmt.Sprintf("marshaling object: %v", err)
		return obs
	}

	// Build patch options
	opts := metav1.PatchOptions{
		FieldManager: FieldManager,
	}
	if force {
		forceVal := true
		opts.Force = &forceVal
	}

	// An observed apply never issues a PATCH on a context that is already
	// done: the request it would answer has ended, and a PATCH that cannot be
	// sent must not be recorded as one that may have been (indeterminate).
	if abandonOnCancel && ctx.Err() != nil {
		result.Action = "failed"
		result.Error = changes.NotAttemptedError
		obs.notSent = true
		return obs
	}

	// Apply via SSA PATCH
	applied, err := dr.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, opts)
	if err != nil {
		result.Action = "failed"
		obs.UID = priorUID
		obs.ErrorClass = changes.ClassifyAPIError(err)
		if apierrors.IsConflict(err) {
			result.Error = fmt.Sprintf("field ownership conflict: %v. Use force to override.", err)
		} else if apierrors.IsForbidden(err) {
			result.Error = fmt.Sprintf("permission denied: %v", err)
		} else if apierrors.IsInvalid(err) {
			result.Error = extractValidationMessage(err)
		} else {
			result.Error = err.Error()
		}
		return obs
	}

	// Determine action: created, configured, or unchanged
	if isNew {
		result.Action = "created"
	} else if existing != nil && existing.GetResourceVersion() == applied.GetResourceVersion() {
		result.Action = "unchanged"
	} else {
		result.Action = "configured"
	}
	obs.UID = applied.GetUID()

	logger.Info("yaml apply",
		"action", result.Action,
		"kind", result.Kind,
		"name", result.Name,
		"namespace", result.Namespace,
	)

	return obs
}

// extractValidationMessage extracts a user-friendly validation error message
// from a Kubernetes StatusError.
func extractValidationMessage(err error) string {
	if statusErr, ok := err.(*apierrors.StatusError); ok {
		return statusErr.Status().Message
	}
	return err.Error()
}
