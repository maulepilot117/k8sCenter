package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// --- Input types ---

// ProviderInput is the input for creating/updating a Flux Provider.
type ProviderInput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"`
	Channel   string `json:"channel"`
	Address   string `json:"address"`
	SecretRef string `json:"secretRef"`
}

// AlertInput is the input for creating/updating a Flux Alert.
type AlertInput struct {
	Name          string           `json:"name"`
	Namespace     string           `json:"namespace"`
	ProviderRef   string           `json:"providerRef"`
	EventSeverity string           `json:"eventSeverity"`
	EventSources  []EventSourceRef `json:"eventSources"`
	InclusionList []string         `json:"inclusionList"`
	ExclusionList []string         `json:"exclusionList"`
}

// ReceiverInput is the input for creating/updating a Flux Receiver.
type ReceiverInput struct {
	Name      string           `json:"name"`
	Namespace string           `json:"namespace"`
	Type      string           `json:"type"`
	Resources []EventSourceRef `json:"resources"`
	SecretRef string           `json:"secretRef"`
}

// --- Helpers ---

// extractEventSources parses a slice of event source references from the given
// nested path in an unstructured object.
func extractEventSources(obj *unstructured.Unstructured, fields ...string) []EventSourceRef {
	raw, found, _ := unstructured.NestedSlice(obj.Object, fields...)
	if !found {
		return []EventSourceRef{}
	}

	sources := make([]EventSourceRef, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		ref := EventSourceRef{}
		if v, ok := m["kind"].(string); ok {
			ref.Kind = v
		}
		if v, ok := m["name"].(string); ok {
			ref.Name = v
		}
		if v, ok := m["namespace"].(string); ok {
			ref.Namespace = v
		}
		if labels, ok := m["matchLabels"].(map[string]interface{}); ok {
			ref.MatchLabels = make(map[string]string, len(labels))
			for k, v := range labels {
				if s, ok := v.(string); ok {
					ref.MatchLabels[k] = s
				}
			}
		}

		sources = append(sources, ref)
	}
	return sources
}

// extractStringSlice parses a slice of strings from the given nested path.
func extractStringSlice(obj *unstructured.Unstructured, fields ...string) []string {
	raw, found, _ := unstructured.NestedSlice(obj.Object, fields...)
	if !found {
		return []string{}
	}

	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// buildEventSourcesSpec converts EventSourceRef slices to unstructured spec format.
func buildEventSourcesSpec(refs []EventSourceRef) []interface{} {
	result := make([]interface{}, 0, len(refs))
	for _, ref := range refs {
		s := map[string]interface{}{
			"kind": ref.Kind,
			"name": ref.Name,
		}
		if ref.Namespace != "" {
			s["namespace"] = ref.Namespace
		}
		if len(ref.MatchLabels) > 0 {
			labels := make(map[string]interface{}, len(ref.MatchLabels))
			for k, v := range ref.MatchLabels {
				labels[k] = v
			}
			s["matchLabels"] = labels
		}
		result = append(result, s)
	}
	return result
}

// buildStringSliceSpec converts a []string to []interface{} for unstructured spec.
func buildStringSliceSpec(ss []string) []interface{} {
	result := make([]interface{}, len(ss))
	for i, v := range ss {
		result[i] = v
	}
	return result
}

// --- Generic CRUD helpers ---

// listResources lists all resources of the given GVR across namespaces and
// normalizes each item using the provided function.
func listResources[T any](ctx context.Context, dynClient dynamic.Interface, gvr schema.GroupVersionResource, normalize func(*unstructured.Unstructured) T) ([]T, error) {
	list, err := dynClient.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, len(list.Items))
	for i := range list.Items {
		result = append(result, normalize(&list.Items[i]))
	}
	return result, nil
}

// deleteResource deletes a namespaced resource by GVR, namespace, and name.
func deleteResource(ctx context.Context, dynClient dynamic.Interface, gvr schema.GroupVersionResource, ns, name string) error {
	return dynClient.Resource(gvr).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// suspendResource patches spec.suspend on a namespaced resource.
func suspendResource(ctx context.Context, dynClient dynamic.Interface, gvr schema.GroupVersionResource, ns, name string, suspend bool) error {
	patchData, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"suspend": suspend,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal suspend patch: %w", err)
	}
	_, err = dynClient.Resource(gvr).Namespace(ns).Patch(ctx, name, types.MergePatchType, patchData, metav1.PatchOptions{})
	return err
}

// --- Validation helpers ---

// validateNameAndNamespace checks that name and namespace are non-empty and
// match RFC 1123 label format.
func validateNameAndNamespace(name, namespace string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !k8sNameRegex.MatchString(name) {
		return fmt.Errorf("invalid resource name: must match [a-z0-9]([a-z0-9-]*[a-z0-9])?")
	}
	if namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if !k8sNameRegex.MatchString(namespace) {
		return fmt.Errorf("invalid namespace: must match [a-z0-9]([a-z0-9-]*[a-z0-9])?")
	}
	return nil
}

// validateEventSourceRefs validates a slice of EventSourceRef entries for count,
// kind, and name constraints.
func validateEventSourceRefs(refs []EventSourceRef, fieldName string) error {
	if len(refs) > 50 {
		return fmt.Errorf("%s: too many entries (max 50)", fieldName)
	}
	for i, ref := range refs {
		if !validEventSourceKinds[ref.Kind] {
			return fmt.Errorf("%s[%d]: unsupported kind %q", fieldName, i, ref.Kind)
		}
		if ref.Name != "*" && ref.Name != "" && !k8sNameRegex.MatchString(ref.Name) {
			return fmt.Errorf("%s[%d]: invalid name %q", fieldName, i, ref.Name)
		}
	}
	return nil
}

// --- Normalize functions ---

// NormalizeProvider extracts fields from a Flux Provider unstructured object
// into a NormalizedProvider.
func NormalizeProvider(obj *unstructured.Unstructured) NormalizedProvider {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	providerType, _, _ := unstructured.NestedString(obj.Object, "spec", "type")
	channel, _, _ := unstructured.NestedString(obj.Object, "spec", "channel")
	address, _, _ := unstructured.NestedString(obj.Object, "spec", "address")
	secretRef, _, _ := unstructured.NestedString(obj.Object, "spec", "secretRef", "name")
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")

	rawConditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	conditions := k8s.ExtractConditions(rawConditions)
	status, message := k8s.MapReadyCondition(conditions)

	if suspended {
		status = "Suspended"
	}

	return NormalizedProvider{
		Name:      name,
		Namespace: namespace,
		Type:      providerType,
		Channel:   channel,
		Address:   address,
		SecretRef: secretRef,
		Suspend:   suspended,
		Status:    status,
		Message:   message,
		CreatedAt: obj.GetCreationTimestamp().Format(time.RFC3339),
	}
}

// NormalizeAlert extracts fields from a Flux Alert unstructured object
// into a NormalizedAlert.
func NormalizeAlert(obj *unstructured.Unstructured) NormalizedAlert {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	providerRef, _, _ := unstructured.NestedString(obj.Object, "spec", "providerRef", "name")
	eventSeverity, _, _ := unstructured.NestedString(obj.Object, "spec", "eventSeverity")
	if eventSeverity == "" {
		eventSeverity = "info"
	}
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")

	// Extract event sources
	eventSources := extractEventSources(obj, "spec", "eventSources")

	// Extract inclusion/exclusion lists
	inclusionList := extractStringSlice(obj, "spec", "inclusionList")
	exclusionList := extractStringSlice(obj, "spec", "exclusionList")

	rawConditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	conditions := k8s.ExtractConditions(rawConditions)
	status, message := k8s.MapReadyCondition(conditions)

	if suspended {
		status = "Suspended"
	}

	return NormalizedAlert{
		Name:          name,
		Namespace:     namespace,
		ProviderRef:   providerRef,
		EventSeverity: eventSeverity,
		EventSources:  eventSources,
		InclusionList: inclusionList,
		ExclusionList: exclusionList,
		Suspend:       suspended,
		Status:        status,
		Message:       message,
		CreatedAt:     obj.GetCreationTimestamp().Format(time.RFC3339),
	}
}

// NormalizeReceiver extracts fields from a Flux Receiver unstructured object
// into a NormalizedReceiver.
func NormalizeReceiver(obj *unstructured.Unstructured) NormalizedReceiver {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	receiverType, _, _ := unstructured.NestedString(obj.Object, "spec", "type")
	secretRef, _, _ := unstructured.NestedString(obj.Object, "spec", "secretRef", "name")
	webhookPath, _, _ := unstructured.NestedString(obj.Object, "status", "webhookPath")
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")

	// Extract resources
	resources := extractEventSources(obj, "spec", "resources")

	rawConditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	conditions := k8s.ExtractConditions(rawConditions)
	status, message := k8s.MapReadyCondition(conditions)

	if suspended {
		status = "Suspended"
	}

	return NormalizedReceiver{
		Name:        name,
		Namespace:   namespace,
		Type:        receiverType,
		Resources:   resources,
		SecretRef:   secretRef,
		Suspend:     suspended,
		WebhookPath: webhookPath,
		Status:      status,
		Message:     message,
		CreatedAt:   obj.GetCreationTimestamp().Format(time.RFC3339),
	}
}

// --- List functions ---

// ListProviders lists all Flux Provider resources across namespaces.
func ListProviders(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedProvider, error) {
	return listResources(ctx, dynClient, FluxProviderGVR, NormalizeProvider)
}

// ListAlerts lists all Flux Alert resources across namespaces.
func ListAlerts(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedAlert, error) {
	return listResources(ctx, dynClient, FluxAlertGVR, NormalizeAlert)
}

// ListReceivers lists all Flux Receiver resources across namespaces.
func ListReceivers(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedReceiver, error) {
	return listResources(ctx, dynClient, FluxReceiverGVR, NormalizeReceiver)
}

// --- Validation functions ---

// ValidateProviderInput validates the fields of a ProviderInput.
func ValidateProviderInput(input ProviderInput) error {
	if err := validateNameAndNamespace(input.Name, input.Namespace); err != nil {
		return err
	}
	if input.Type == "" {
		return fmt.Errorf("type is required")
	}
	if !validProviderTypes[input.Type] {
		return fmt.Errorf("unsupported provider type: %s", input.Type)
	}
	if input.Address != "" {
		u, err := url.Parse(input.Address)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid address: must be a valid http or https URL")
		}
	}
	if len(input.Channel) > 512 {
		return fmt.Errorf("channel too long (max 512 characters)")
	}
	if input.SecretRef != "" && !k8sNameRegex.MatchString(input.SecretRef) {
		return fmt.Errorf("invalid secretRef: must match RFC 1123")
	}
	return nil
}

// ValidateAlertInput validates the fields of an AlertInput.
func ValidateAlertInput(input AlertInput) error {
	if err := validateNameAndNamespace(input.Name, input.Namespace); err != nil {
		return err
	}
	if input.EventSeverity != "" && !validEventSeverities[input.EventSeverity] {
		return fmt.Errorf("eventSeverity must be 'info' or 'error'")
	}
	if input.ProviderRef == "" {
		return fmt.Errorf("providerRef is required")
	}
	if !k8sNameRegex.MatchString(input.ProviderRef) {
		return fmt.Errorf("invalid providerRef: must match RFC 1123")
	}
	if len(input.EventSources) == 0 {
		return fmt.Errorf("at least one event source is required")
	}
	if err := validateEventSourceRefs(input.EventSources, "eventSources"); err != nil {
		return err
	}
	if len(input.InclusionList) > 50 {
		return fmt.Errorf("inclusionList: too many entries (max 50)")
	}
	if len(input.ExclusionList) > 50 {
		return fmt.Errorf("exclusionList: too many entries (max 50)")
	}
	return nil
}

// ValidateReceiverInput validates the fields of a ReceiverInput.
func ValidateReceiverInput(input ReceiverInput) error {
	if err := validateNameAndNamespace(input.Name, input.Namespace); err != nil {
		return err
	}
	if input.Type == "" {
		return fmt.Errorf("type is required")
	}
	if !validReceiverTypes[input.Type] {
		return fmt.Errorf("unsupported receiver type: %s", input.Type)
	}
	if input.SecretRef == "" {
		return fmt.Errorf("secretRef is required")
	}
	if !k8sNameRegex.MatchString(input.SecretRef) {
		return fmt.Errorf("invalid secretRef: must match RFC 1123")
	}
	if len(input.Resources) == 0 {
		return fmt.Errorf("at least one resource is required")
	}
	if err := validateEventSourceRefs(input.Resources, "resources"); err != nil {
		return err
	}
	return nil
}

// --- CRUD: Provider ---

// CreateProvider creates a new Flux Provider resource.
func CreateProvider(ctx context.Context, dynClient dynamic.Interface, ns string, input ProviderInput) (*NormalizedProvider, error) {
	spec := map[string]interface{}{
		"type": input.Type,
	}
	if input.Channel != "" {
		spec["channel"] = input.Channel
	}
	if input.Address != "" {
		spec["address"] = input.Address
	}
	if input.SecretRef != "" {
		spec["secretRef"] = map[string]interface{}{
			"name": input.SecretRef,
		}
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1beta3",
			"kind":       "Provider",
			"metadata": map[string]interface{}{
				"name":      input.Name,
				"namespace": ns,
				"labels": map[string]interface{}{
					managedByLabel: managedByValue,
				},
			},
			"spec": spec,
		},
	}

	created, err := dynClient.Resource(FluxProviderGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating flux provider: %w", err)
	}

	result := NormalizeProvider(created)
	return &result, nil
}

// UpdateProvider updates an existing Flux Provider resource.
func UpdateProvider(ctx context.Context, dynClient dynamic.Interface, ns, name string, input ProviderInput) (*NormalizedProvider, error) {
	existing, err := dynClient.Resource(FluxProviderGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting flux provider %s/%s: %w", ns, name, err)
	}

	// Merge into existing spec to preserve fields not modeled by ProviderInput
	// (e.g., suspend, proxy, certSecretRef, timeout, username).
	existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	if existingSpec == nil {
		existingSpec = make(map[string]interface{})
	}

	existingSpec["type"] = input.Type
	if input.Channel != "" {
		existingSpec["channel"] = input.Channel
	} else {
		delete(existingSpec, "channel")
	}
	if input.Address != "" {
		existingSpec["address"] = input.Address
	} else {
		delete(existingSpec, "address")
	}
	if input.SecretRef != "" {
		existingSpec["secretRef"] = map[string]interface{}{
			"name": input.SecretRef,
		}
	} else {
		delete(existingSpec, "secretRef")
	}

	if err := unstructured.SetNestedField(existing.Object, existingSpec, "spec"); err != nil {
		return nil, fmt.Errorf("setting spec on provider: %w", err)
	}

	updated, err := dynClient.Resource(FluxProviderGVR).Namespace(ns).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("updating flux provider %s/%s: %w", ns, name, err)
	}

	result := NormalizeProvider(updated)
	return &result, nil
}

// DeleteProvider deletes a Flux Provider resource.
func DeleteProvider(ctx context.Context, dynClient dynamic.Interface, ns, name string) error {
	return deleteResource(ctx, dynClient, FluxProviderGVR, ns, name)
}

// SuspendProvider suspends or resumes a Flux Provider by patching spec.suspend.
func SuspendProvider(ctx context.Context, dynClient dynamic.Interface, ns, name string, suspend bool) error {
	return suspendResource(ctx, dynClient, FluxProviderGVR, ns, name, suspend)
}

// --- CRUD: Alert ---

// CreateAlert creates a new Flux Alert resource.
func CreateAlert(ctx context.Context, dynClient dynamic.Interface, ns string, input AlertInput) (*NormalizedAlert, error) {
	eventSeverity := input.EventSeverity
	if eventSeverity == "" {
		eventSeverity = "info"
	}

	spec := map[string]interface{}{
		"providerRef": map[string]interface{}{
			"name": input.ProviderRef,
		},
		"eventSeverity": eventSeverity,
		"eventSources":  buildEventSourcesSpec(input.EventSources),
	}

	if len(input.InclusionList) > 0 {
		spec["inclusionList"] = buildStringSliceSpec(input.InclusionList)
	}
	if len(input.ExclusionList) > 0 {
		spec["exclusionList"] = buildStringSliceSpec(input.ExclusionList)
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1beta3",
			"kind":       "Alert",
			"metadata": map[string]interface{}{
				"name":      input.Name,
				"namespace": ns,
				"labels": map[string]interface{}{
					managedByLabel: managedByValue,
				},
			},
			"spec": spec,
		},
	}

	created, err := dynClient.Resource(FluxAlertGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating flux alert: %w", err)
	}

	result := NormalizeAlert(created)
	return &result, nil
}

// UpdateAlert updates an existing Flux Alert resource.
func UpdateAlert(ctx context.Context, dynClient dynamic.Interface, ns, name string, input AlertInput) (*NormalizedAlert, error) {
	existing, err := dynClient.Resource(FluxAlertGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting flux alert %s/%s: %w", ns, name, err)
	}

	eventSeverity := input.EventSeverity
	if eventSeverity == "" {
		eventSeverity = "info"
	}

	// Merge into existing spec to preserve fields not modeled by AlertInput
	// (e.g., suspend, summary).
	existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	if existingSpec == nil {
		existingSpec = make(map[string]interface{})
	}

	existingSpec["providerRef"] = map[string]interface{}{
		"name": input.ProviderRef,
	}
	existingSpec["eventSeverity"] = eventSeverity
	existingSpec["eventSources"] = buildEventSourcesSpec(input.EventSources)

	if len(input.InclusionList) > 0 {
		existingSpec["inclusionList"] = buildStringSliceSpec(input.InclusionList)
	} else {
		delete(existingSpec, "inclusionList")
	}
	if len(input.ExclusionList) > 0 {
		existingSpec["exclusionList"] = buildStringSliceSpec(input.ExclusionList)
	} else {
		delete(existingSpec, "exclusionList")
	}

	if err := unstructured.SetNestedField(existing.Object, existingSpec, "spec"); err != nil {
		return nil, fmt.Errorf("setting spec on alert: %w", err)
	}

	updated, err := dynClient.Resource(FluxAlertGVR).Namespace(ns).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("updating flux alert %s/%s: %w", ns, name, err)
	}

	result := NormalizeAlert(updated)
	return &result, nil
}

// DeleteAlert deletes a Flux Alert resource.
func DeleteAlert(ctx context.Context, dynClient dynamic.Interface, ns, name string) error {
	return deleteResource(ctx, dynClient, FluxAlertGVR, ns, name)
}

// SuspendAlert suspends or resumes a Flux Alert by patching spec.suspend.
func SuspendAlert(ctx context.Context, dynClient dynamic.Interface, ns, name string, suspend bool) error {
	return suspendResource(ctx, dynClient, FluxAlertGVR, ns, name, suspend)
}

// --- CRUD: Receiver ---

// CreateReceiver creates a new Flux Receiver resource.
func CreateReceiver(ctx context.Context, dynClient dynamic.Interface, ns string, input ReceiverInput) (*NormalizedReceiver, error) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1",
			"kind":       "Receiver",
			"metadata": map[string]interface{}{
				"name":      input.Name,
				"namespace": ns,
				"labels": map[string]interface{}{
					managedByLabel: managedByValue,
				},
			},
			"spec": map[string]interface{}{
				"type": input.Type,
				"secretRef": map[string]interface{}{
					"name": input.SecretRef,
				},
				"resources": buildEventSourcesSpec(input.Resources),
			},
		},
	}

	created, err := dynClient.Resource(FluxReceiverGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating flux receiver: %w", err)
	}

	result := NormalizeReceiver(created)
	return &result, nil
}

// UpdateReceiver updates an existing Flux Receiver resource.
func UpdateReceiver(ctx context.Context, dynClient dynamic.Interface, ns, name string, input ReceiverInput) (*NormalizedReceiver, error) {
	existing, err := dynClient.Resource(FluxReceiverGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting flux receiver %s/%s: %w", ns, name, err)
	}

	// Merge into existing spec to preserve fields not modeled by ReceiverInput
	// (e.g., suspend, interval).
	existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	if existingSpec == nil {
		existingSpec = make(map[string]interface{})
	}

	existingSpec["type"] = input.Type
	existingSpec["secretRef"] = map[string]interface{}{
		"name": input.SecretRef,
	}
	existingSpec["resources"] = buildEventSourcesSpec(input.Resources)

	if err := unstructured.SetNestedField(existing.Object, existingSpec, "spec"); err != nil {
		return nil, fmt.Errorf("setting spec on receiver: %w", err)
	}

	updated, err := dynClient.Resource(FluxReceiverGVR).Namespace(ns).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("updating flux receiver %s/%s: %w", ns, name, err)
	}

	result := NormalizeReceiver(updated)
	return &result, nil
}

// DeleteReceiver deletes a Flux Receiver resource.
func DeleteReceiver(ctx context.Context, dynClient dynamic.Interface, ns, name string) error {
	return deleteResource(ctx, dynClient, FluxReceiverGVR, ns, name)
}

// SuspendReceiver suspends or resumes a Flux Receiver by patching spec.suspend.
func SuspendReceiver(ctx context.Context, dynClient dynamic.Interface, ns, name string, suspend bool) error {
	return suspendResource(ctx, dynClient, FluxReceiverGVR, ns, name, suspend)
}
