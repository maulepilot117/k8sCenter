package notification

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// makeUnstructured is a test helper that wraps a raw map into an Unstructured object.
func makeUnstructured(t *testing.T, obj map[string]interface{}) *unstructured.Unstructured {
	t.Helper()
	return &unstructured.Unstructured{Object: obj}
}

// readyCondition returns a Ready=True condition with the given message.
func readyCondition(message string) map[string]interface{} {
	return map[string]interface{}{
		"type":    "Ready",
		"status":  "True",
		"message": message,
	}
}

// notReadyCondition returns a Ready=False condition with the given message.
func notReadyCondition(message string) map[string]interface{} {
	return map[string]interface{}{
		"type":    "Ready",
		"status":  "False",
		"message": message,
	}
}

// --- TestNormalizeProvider ---

func TestNormalizeProvider(t *testing.T) {
	t.Run("ready provider", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1beta3",
			"kind":       "Provider",
			"metadata": map[string]interface{}{
				"name":              "test-provider",
				"namespace":         "flux-system",
				"creationTimestamp": "2024-01-01T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"type":    "slack",
				"channel": "#alerts",
				"secretRef": map[string]interface{}{
					"name": "slack-token",
				},
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					readyCondition("ok"),
				},
			},
		})

		p := NormalizeProvider(obj)

		assertEqual(t, "Name", p.Name, "test-provider")
		assertEqual(t, "Namespace", p.Namespace, "flux-system")
		assertEqual(t, "Type", p.Type, "slack")
		assertEqual(t, "Channel", p.Channel, "#alerts")
		assertEqual(t, "SecretRef", p.SecretRef, "slack-token")
		assertEqual(t, "Status", p.Status, "Ready")
		assertEqual(t, "Message", p.Message, "ok")
		assertEqual(t, "Suspend", p.Suspend, false)

		if p.CreatedAt == "" {
			t.Error("expected non-empty CreatedAt")
		}
	})

	t.Run("not ready provider", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1beta3",
			"kind":       "Provider",
			"metadata": map[string]interface{}{
				"name":              "failing-provider",
				"namespace":         "flux-system",
				"creationTimestamp": "2024-01-01T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"type": "slack",
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					notReadyCondition("connection failed"),
				},
			},
		})

		p := NormalizeProvider(obj)

		assertEqual(t, "Status", p.Status, "Not Ready")
		assertEqual(t, "Message", p.Message, "connection failed")
	})

	t.Run("suspended provider overrides ready", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1beta3",
			"kind":       "Provider",
			"metadata": map[string]interface{}{
				"name":              "suspended-provider",
				"namespace":         "flux-system",
				"creationTimestamp": "2024-01-01T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"type":    "slack",
				"suspend": true,
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					readyCondition("ok"),
				},
			},
		})

		p := NormalizeProvider(obj)

		assertEqual(t, "Status", p.Status, "Suspended")
		assertEqual(t, "Suspend", p.Suspend, true)
	})
}

// --- TestNormalizeAlert ---

func TestNormalizeAlert(t *testing.T) {
	t.Run("alert with all fields", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1beta3",
			"kind":       "Alert",
			"metadata": map[string]interface{}{
				"name":              "test-alert",
				"namespace":         "flux-system",
				"creationTimestamp": "2024-01-15T10:00:00Z",
			},
			"spec": map[string]interface{}{
				"providerRef": map[string]interface{}{
					"name": "slack-provider",
				},
				"eventSeverity": "error",
				"eventSources": []interface{}{
					map[string]interface{}{
						"kind":      "Kustomization",
						"name":      "app1",
						"namespace": "default",
					},
					map[string]interface{}{
						"kind":      "HelmRelease",
						"name":      "app2",
						"namespace": "production",
					},
				},
				"inclusionList": []interface{}{".*failed.*", ".*error.*"},
				"exclusionList": []interface{}{".*info.*"},
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					readyCondition("initialized"),
				},
			},
		})

		a := NormalizeAlert(obj)

		assertEqual(t, "Name", a.Name, "test-alert")
		assertEqual(t, "Namespace", a.Namespace, "flux-system")
		assertEqual(t, "ProviderRef", a.ProviderRef, "slack-provider")
		assertEqual(t, "EventSeverity", a.EventSeverity, "error")
		assertEqual(t, "Status", a.Status, "Ready")

		if len(a.EventSources) != 2 {
			t.Fatalf("expected 2 event sources, got %d", len(a.EventSources))
		}
		assertEqual(t, "EventSources[0].Kind", a.EventSources[0].Kind, "Kustomization")
		assertEqual(t, "EventSources[0].Name", a.EventSources[0].Name, "app1")
		assertEqual(t, "EventSources[0].Namespace", a.EventSources[0].Namespace, "default")
		assertEqual(t, "EventSources[1].Kind", a.EventSources[1].Kind, "HelmRelease")
		assertEqual(t, "EventSources[1].Name", a.EventSources[1].Name, "app2")

		if len(a.InclusionList) != 2 {
			t.Fatalf("expected 2 inclusion entries, got %d", len(a.InclusionList))
		}
		assertEqual(t, "InclusionList[0]", a.InclusionList[0], ".*failed.*")
		assertEqual(t, "InclusionList[1]", a.InclusionList[1], ".*error.*")

		if len(a.ExclusionList) != 1 {
			t.Fatalf("expected 1 exclusion entry, got %d", len(a.ExclusionList))
		}
		assertEqual(t, "ExclusionList[0]", a.ExclusionList[0], ".*info.*")
	})

	t.Run("empty eventSeverity defaults to info", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1beta3",
			"kind":       "Alert",
			"metadata": map[string]interface{}{
				"name":              "default-severity-alert",
				"namespace":         "flux-system",
				"creationTimestamp": "2024-01-01T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"providerRef": map[string]interface{}{
					"name": "slack-provider",
				},
				"eventSources": []interface{}{
					map[string]interface{}{
						"kind": "Kustomization",
						"name": "*",
					},
				},
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					readyCondition("ok"),
				},
			},
		})

		a := NormalizeAlert(obj)

		assertEqual(t, "EventSeverity", a.EventSeverity, "info")
	})

	t.Run("suspended alert", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1beta3",
			"kind":       "Alert",
			"metadata": map[string]interface{}{
				"name":              "suspended-alert",
				"namespace":         "flux-system",
				"creationTimestamp": "2024-01-01T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"providerRef": map[string]interface{}{
					"name": "slack-provider",
				},
				"eventSources": []interface{}{
					map[string]interface{}{
						"kind": "Kustomization",
						"name": "*",
					},
				},
				"suspend": true,
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					readyCondition("ok"),
				},
			},
		})

		a := NormalizeAlert(obj)

		assertEqual(t, "Status", a.Status, "Suspended")
		assertEqual(t, "Suspend", a.Suspend, true)
	})
}

// --- TestNormalizeReceiver ---

func TestNormalizeReceiver(t *testing.T) {
	t.Run("receiver with webhookPath", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1",
			"kind":       "Receiver",
			"metadata": map[string]interface{}{
				"name":              "github-receiver",
				"namespace":         "flux-system",
				"creationTimestamp": "2024-02-01T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"type": "github",
				"secretRef": map[string]interface{}{
					"name": "github-webhook-secret",
				},
				"resources": []interface{}{
					map[string]interface{}{
						"kind": "GitRepository",
						"name": "my-repo",
					},
				},
			},
			"status": map[string]interface{}{
				"webhookPath": "/hook/abc123def456",
				"conditions": []interface{}{
					readyCondition("receiver initialized"),
				},
			},
		})

		r := NormalizeReceiver(obj)

		assertEqual(t, "Name", r.Name, "github-receiver")
		assertEqual(t, "Namespace", r.Namespace, "flux-system")
		assertEqual(t, "Type", r.Type, "github")
		assertEqual(t, "SecretRef", r.SecretRef, "github-webhook-secret")
		assertEqual(t, "WebhookPath", r.WebhookPath, "/hook/abc123def456")
		assertEqual(t, "Status", r.Status, "Ready")
		assertEqual(t, "Message", r.Message, "receiver initialized")

		if len(r.Resources) != 1 {
			t.Fatalf("expected 1 resource, got %d", len(r.Resources))
		}
		assertEqual(t, "Resources[0].Kind", r.Resources[0].Kind, "GitRepository")
		assertEqual(t, "Resources[0].Name", r.Resources[0].Name, "my-repo")
	})

	t.Run("receiver with empty webhookPath not yet reconciled", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1",
			"kind":       "Receiver",
			"metadata": map[string]interface{}{
				"name":              "new-receiver",
				"namespace":         "flux-system",
				"creationTimestamp": "2024-02-01T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"type": "generic",
				"secretRef": map[string]interface{}{
					"name": "webhook-secret",
				},
				"resources": []interface{}{
					map[string]interface{}{
						"kind": "Kustomization",
						"name": "*",
					},
				},
			},
		})

		r := NormalizeReceiver(obj)

		assertEqual(t, "WebhookPath", r.WebhookPath, "")
		// No conditions means Unknown status
		assertEqual(t, "Status", r.Status, "Unknown")
	})

	t.Run("receiver without suspend field normalizes fine", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"apiVersion": "notification.toolkit.fluxcd.io/v1",
			"kind":       "Receiver",
			"metadata": map[string]interface{}{
				"name":              "plain-receiver",
				"namespace":         "default",
				"creationTimestamp": "2024-03-01T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"type": "dockerhub",
				"secretRef": map[string]interface{}{
					"name": "dockerhub-secret",
				},
				"resources": []interface{}{
					map[string]interface{}{
						"kind":      "ImageRepository",
						"name":      "my-image",
						"namespace": "flux-system",
					},
				},
			},
			"status": map[string]interface{}{
				"webhookPath": "/hook/xyz",
				"conditions": []interface{}{
					readyCondition("ok"),
				},
			},
		})

		r := NormalizeReceiver(obj)

		assertEqual(t, "Name", r.Name, "plain-receiver")
		assertEqual(t, "Type", r.Type, "dockerhub")
		assertEqual(t, "Status", r.Status, "Ready")
	})
}

// --- TestValidateProviderInput ---

func TestValidateProviderInput(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		input := ProviderInput{
			Name:      "my-provider",
			Namespace: "flux-system",
			Type:      "slack",
			Channel:   "#alerts",
			SecretRef: "slack-token",
		}
		if err := ValidateProviderInput(input); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		input := ProviderInput{
			Name: "",
			Type: "slack",
		}
		err := ValidateProviderInput(input)
		assertErrorContains(t, err, "name is required")
	})

	t.Run("invalid name uppercase", func(t *testing.T) {
		input := ProviderInput{
			Name: "MyProvider",
			Type: "slack",
		}
		err := ValidateProviderInput(input)
		assertErrorContains(t, err, "invalid resource name")
	})

	t.Run("empty type", func(t *testing.T) {
		input := ProviderInput{
			Name:      "my-provider",
			Namespace: "flux-system",
			Type:      "",
		}
		err := ValidateProviderInput(input)
		assertErrorContains(t, err, "type is required")
	})

	t.Run("invalid type", func(t *testing.T) {
		input := ProviderInput{
			Name:      "my-provider",
			Namespace: "flux-system",
			Type:      "foobar",
		}
		err := ValidateProviderInput(input)
		assertErrorContains(t, err, "unsupported provider type")
	})
}

// --- TestValidateAlertInput ---

func TestValidateAlertInput(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		input := AlertInput{
			Name:        "my-alert",
			Namespace:   "flux-system",
			ProviderRef: "slack-provider",
			EventSources: []EventSourceRef{
				{Kind: "Kustomization", Name: "*"},
			},
		}
		if err := ValidateAlertInput(input); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("empty providerRef", func(t *testing.T) {
		input := AlertInput{
			Name:        "my-alert",
			Namespace:   "flux-system",
			ProviderRef: "",
			EventSources: []EventSourceRef{
				{Kind: "Kustomization", Name: "*"},
			},
		}
		err := ValidateAlertInput(input)
		assertErrorContains(t, err, "providerRef is required")
	})

	t.Run("empty event sources", func(t *testing.T) {
		input := AlertInput{
			Name:         "my-alert",
			Namespace:    "flux-system",
			ProviderRef:  "slack-provider",
			EventSources: []EventSourceRef{},
		}
		err := ValidateAlertInput(input)
		assertErrorContains(t, err, "at least one event source")
	})
}

// --- TestValidateReceiverInput ---

func TestValidateReceiverInput(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		input := ReceiverInput{
			Name:      "my-receiver",
			Namespace: "flux-system",
			Type:      "github",
			SecretRef: "webhook-secret",
			Resources: []EventSourceRef{
				{Kind: "GitRepository", Name: "my-repo"},
			},
		}
		if err := ValidateReceiverInput(input); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("invalid type", func(t *testing.T) {
		input := ReceiverInput{
			Name:      "my-receiver",
			Namespace: "flux-system",
			Type:      "foobar",
			SecretRef: "webhook-secret",
			Resources: []EventSourceRef{
				{Kind: "GitRepository", Name: "my-repo"},
			},
		}
		err := ValidateReceiverInput(input)
		assertErrorContains(t, err, "unsupported receiver type")
	})

	t.Run("empty secretRef", func(t *testing.T) {
		input := ReceiverInput{
			Name:      "my-receiver",
			Namespace: "flux-system",
			Type:      "github",
			SecretRef: "",
			Resources: []EventSourceRef{
				{Kind: "GitRepository", Name: "my-repo"},
			},
		}
		err := ValidateReceiverInput(input)
		assertErrorContains(t, err, "secretRef is required")
	})

	t.Run("empty resources", func(t *testing.T) {
		input := ReceiverInput{
			Name:      "my-receiver",
			Namespace: "flux-system",
			Type:      "github",
			SecretRef: "webhook-secret",
			Resources: []EventSourceRef{},
		}
		err := ValidateReceiverInput(input)
		assertErrorContains(t, err, "at least one resource")
	})
}

// --- TestExtractEventSources ---

func TestExtractEventSources(t *testing.T) {
	t.Run("two entries with all fields", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"spec": map[string]interface{}{
				"eventSources": []interface{}{
					map[string]interface{}{
						"kind":      "Kustomization",
						"name":      "app1",
						"namespace": "default",
						"matchLabels": map[string]interface{}{
							"env": "prod",
						},
					},
					map[string]interface{}{
						"kind":      "HelmRelease",
						"name":      "app2",
						"namespace": "staging",
						"matchLabels": map[string]interface{}{
							"team": "platform",
						},
					},
				},
			},
		})

		sources := extractEventSources(obj, "spec", "eventSources")

		if len(sources) != 2 {
			t.Fatalf("expected 2 event sources, got %d", len(sources))
		}

		assertEqual(t, "sources[0].Kind", sources[0].Kind, "Kustomization")
		assertEqual(t, "sources[0].Name", sources[0].Name, "app1")
		assertEqual(t, "sources[0].Namespace", sources[0].Namespace, "default")
		if sources[0].MatchLabels["env"] != "prod" {
			t.Errorf("expected matchLabel env=prod, got %v", sources[0].MatchLabels)
		}

		assertEqual(t, "sources[1].Kind", sources[1].Kind, "HelmRelease")
		assertEqual(t, "sources[1].Name", sources[1].Name, "app2")
		assertEqual(t, "sources[1].Namespace", sources[1].Namespace, "staging")
		if sources[1].MatchLabels["team"] != "platform" {
			t.Errorf("expected matchLabel team=platform, got %v", sources[1].MatchLabels)
		}
	})

	t.Run("missing path returns empty slice", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"spec": map[string]interface{}{},
		})

		sources := extractEventSources(obj, "spec", "eventSources")

		if sources == nil {
			t.Fatal("expected non-nil empty slice, got nil")
		}
		if len(sources) != 0 {
			t.Fatalf("expected 0 event sources, got %d", len(sources))
		}
	})

	t.Run("empty slice returns empty slice", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"spec": map[string]interface{}{
				"eventSources": []interface{}{},
			},
		})

		sources := extractEventSources(obj, "spec", "eventSources")

		if sources == nil {
			t.Fatal("expected non-nil empty slice, got nil")
		}
		if len(sources) != 0 {
			t.Fatalf("expected 0 event sources, got %d", len(sources))
		}
	})
}

// --- TestExtractStringSlice ---

func TestExtractStringSlice(t *testing.T) {
	t.Run("two string entries", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"spec": map[string]interface{}{
				"inclusionList": []interface{}{".*failed.*", ".*error.*"},
			},
		})

		result := extractStringSlice(obj, "spec", "inclusionList")

		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		assertEqual(t, "result[0]", result[0], ".*failed.*")
		assertEqual(t, "result[1]", result[1], ".*error.*")
	})

	t.Run("missing path returns empty slice", func(t *testing.T) {
		obj := makeUnstructured(t, map[string]interface{}{
			"spec": map[string]interface{}{},
		})

		result := extractStringSlice(obj, "spec", "inclusionList")

		if result == nil {
			t.Fatal("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Fatalf("expected 0 entries, got %d", len(result))
		}
	})
}

// --- Test helpers ---

// assertEqual is a generic test helper that compares two comparable values.
func assertEqual[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", field, got, want)
	}
}

// assertErrorContains verifies that err is non-nil and its message contains substr.
func assertErrorContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("expected error containing %q, got: %v", substr, err)
	}
}
