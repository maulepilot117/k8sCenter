package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

// mapReadyCondition extracts the Ready condition from a conditions list
// and returns a status string and message. If the resource is suspended,
// the caller should override the returned status to "Suspended".
func mapReadyCondition(conditions []map[string]string) (string, string) {
	for _, c := range conditions {
		if c["type"] != "Ready" {
			continue
		}
		switch c["status"] {
		case "True":
			return "Ready", c["message"]
		case "False":
			return "Not Ready", c["message"]
		}
	}
	return "Unknown", ""
}

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

	conditions := extractConditions(obj)
	status, message := mapReadyCondition(conditions)

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

	conditions := extractConditions(obj)
	status, message := mapReadyCondition(conditions)

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

	// Extract resources
	resources := extractEventSources(obj, "spec", "resources")

	conditions := extractConditions(obj)
	status, message := mapReadyCondition(conditions)

	return NormalizedReceiver{
		Name:        name,
		Namespace:   namespace,
		Type:        receiverType,
		Resources:   resources,
		SecretRef:   secretRef,
		WebhookPath: webhookPath,
		Status:      status,
		Message:     message,
		CreatedAt:   obj.GetCreationTimestamp().Format(time.RFC3339),
	}
}

// --- List functions ---

// ListProviders lists all Flux Provider resources across namespaces.
func ListProviders(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedProvider, error) {
	list, err := dynClient.Resource(FluxProviderGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing flux providers: %w", err)
	}

	providers := make([]NormalizedProvider, 0, len(list.Items))
	for i := range list.Items {
		providers = append(providers, NormalizeProvider(&list.Items[i]))
	}
	return providers, nil
}

// ListAlerts lists all Flux Alert resources across namespaces.
func ListAlerts(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedAlert, error) {
	list, err := dynClient.Resource(FluxAlertGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing flux alerts: %w", err)
	}

	alerts := make([]NormalizedAlert, 0, len(list.Items))
	for i := range list.Items {
		alerts = append(alerts, NormalizeAlert(&list.Items[i]))
	}
	return alerts, nil
}

// ListReceivers lists all Flux Receiver resources across namespaces.
func ListReceivers(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedReceiver, error) {
	list, err := dynClient.Resource(FluxReceiverGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing flux receivers: %w", err)
	}

	receivers := make([]NormalizedReceiver, 0, len(list.Items))
	for i := range list.Items {
		receivers = append(receivers, NormalizeReceiver(&list.Items[i]))
	}
	return receivers, nil
}

// --- Validation functions ---

// ValidateProviderInput validates the fields of a ProviderInput.
func ValidateProviderInput(input ProviderInput) error {
	if input.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !k8sNameRegex.MatchString(input.Name) {
		return fmt.Errorf("invalid resource name: must match [a-z0-9]([a-z0-9-]*[a-z0-9])?")
	}
	if input.Type == "" {
		return fmt.Errorf("type is required")
	}
	if !validProviderTypes[input.Type] {
		return fmt.Errorf("unsupported provider type: %s", input.Type)
	}
	return nil
}

// ValidateAlertInput validates the fields of an AlertInput.
func ValidateAlertInput(input AlertInput) error {
	if input.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !k8sNameRegex.MatchString(input.Name) {
		return fmt.Errorf("invalid resource name: must match [a-z0-9]([a-z0-9-]*[a-z0-9])?")
	}
	if input.ProviderRef == "" {
		return fmt.Errorf("providerRef is required")
	}
	if len(input.EventSources) == 0 {
		return fmt.Errorf("at least one event source is required")
	}
	return nil
}

// ValidateReceiverInput validates the fields of a ReceiverInput.
func ValidateReceiverInput(input ReceiverInput) error {
	if input.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !k8sNameRegex.MatchString(input.Name) {
		return fmt.Errorf("invalid resource name: must match [a-z0-9]([a-z0-9-]*[a-z0-9])?")
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
	if len(input.Resources) == 0 {
		return fmt.Errorf("at least one resource is required")
	}
	return nil
}

// --- CRUD: Provider ---

// CreateProvider creates a new Flux Provider resource.
func CreateProvider(ctx context.Context, dynClient dynamic.Interface, ns string, input ProviderInput) (*NormalizedProvider, error) {
	if err := ValidateProviderInput(input); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

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
	if err := ValidateProviderInput(input); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	existing, err := dynClient.Resource(FluxProviderGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting flux provider %s/%s: %w", ns, name, err)
	}

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

	if err := unstructured.SetNestedField(existing.Object, spec, "spec"); err != nil {
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
	return dynClient.Resource(FluxProviderGVR).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// SuspendProvider suspends or resumes a Flux Provider by patching spec.suspend.
func SuspendProvider(ctx context.Context, dynClient dynamic.Interface, ns, name string, suspend bool) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"suspend": suspend,
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling suspend patch: %w", err)
	}

	_, err = dynClient.Resource(FluxProviderGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patching flux provider %s/%s suspend=%v: %w", ns, name, suspend, err)
	}

	return nil
}

// --- CRUD: Alert ---

// CreateAlert creates a new Flux Alert resource.
func CreateAlert(ctx context.Context, dynClient dynamic.Interface, ns string, input AlertInput) (*NormalizedAlert, error) {
	if err := ValidateAlertInput(input); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

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
	if err := ValidateAlertInput(input); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	existing, err := dynClient.Resource(FluxAlertGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting flux alert %s/%s: %w", ns, name, err)
	}

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

	if err := unstructured.SetNestedField(existing.Object, spec, "spec"); err != nil {
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
	return dynClient.Resource(FluxAlertGVR).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// SuspendAlert suspends or resumes a Flux Alert by patching spec.suspend.
func SuspendAlert(ctx context.Context, dynClient dynamic.Interface, ns, name string, suspend bool) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"suspend": suspend,
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling suspend patch: %w", err)
	}

	_, err = dynClient.Resource(FluxAlertGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patching flux alert %s/%s suspend=%v: %w", ns, name, suspend, err)
	}

	return nil
}

// --- CRUD: Receiver ---

// CreateReceiver creates a new Flux Receiver resource.
func CreateReceiver(ctx context.Context, dynClient dynamic.Interface, ns string, input ReceiverInput) (*NormalizedReceiver, error) {
	if err := ValidateReceiverInput(input); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

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
	if err := ValidateReceiverInput(input); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	existing, err := dynClient.Resource(FluxReceiverGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting flux receiver %s/%s: %w", ns, name, err)
	}

	spec := map[string]interface{}{
		"type": input.Type,
		"secretRef": map[string]interface{}{
			"name": input.SecretRef,
		},
		"resources": buildEventSourcesSpec(input.Resources),
	}

	if err := unstructured.SetNestedField(existing.Object, spec, "spec"); err != nil {
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
	return dynClient.Resource(FluxReceiverGVR).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
}
