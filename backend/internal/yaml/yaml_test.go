package yaml

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// --- Security Tests ---

func TestCheckSecurity_EmptyBody(t *testing.T) {
	err := CheckSecurity([]byte{})
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckSecurity_TooLarge(t *testing.T) {
	data := make([]byte, MaxBodySize+1)
	for i := range data {
		data[i] = 'a'
	}
	err := CheckSecurity(data)
	if err == nil {
		t.Fatal("expected error for oversized body")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckSecurity_UnsafeTags(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"python", "data: !!python/object:os.system 'ls'"},
		{"ruby", "data: !!ruby/object:Gem::Installer"},
		{"js", "data: !!js/function 'alert(1)'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckSecurity([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error for unsafe tag")
			}
			if !strings.Contains(err.Error(), "unsafe YAML tag") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCheckSecurity_AnchorsAliasesAllowed(t *testing.T) {
	// Anchors/aliases are allowed in pre-parse security checks.
	// YAML bomb protection is handled post-parse by CheckExpansionRatio.
	yaml := `
a: &anchor
  key: value
b: *anchor
`
	err := CheckSecurity([]byte(yaml))
	if err != nil {
		t.Fatalf("expected anchors/aliases to be allowed, got: %v", err)
	}
}

func TestCheckSecurity_URLsWithAmpersand(t *testing.T) {
	// Verify URLs with & are not rejected (was a false positive before)
	yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  url: "https://example.com?foo&bar"
`
	err := CheckSecurity([]byte(yaml))
	if err != nil {
		t.Fatalf("expected URL with & to be allowed, got: %v", err)
	}
}

func TestCheckSecurity_ValidYAML(t *testing.T) {
	yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  key: value
`
	err := CheckSecurity([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckSecurity_CommentsWithStarNotRejected(t *testing.T) {
	// Comments containing * should not be rejected
	yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
# This is a comment with *star and &ampersand
data:
  key: value
`
	err := CheckSecurity([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error for comment with star: %v", err)
	}
}

func TestCheckExpansionRatio_Normal(t *testing.T) {
	err := CheckExpansionRatio(100, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckExpansionRatio_Bomb(t *testing.T) {
	err := CheckExpansionRatio(100, 20000)
	if err == nil {
		t.Fatal("expected error for expansion bomb")
	}
	if !strings.Contains(err.Error(), "expansion ratio") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Parser Tests ---

func TestParseMultiDoc_SingleDocument(t *testing.T) {
	yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  key: value
`
	docs, err := ParseMultiDoc([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].GetKind() != "ConfigMap" {
		t.Fatalf("expected kind ConfigMap, got %s", docs[0].GetKind())
	}
	if docs[0].GetName() != "test" {
		t.Fatalf("expected name test, got %s", docs[0].GetName())
	}
}

func TestParseMultiDoc_MultipleDocuments(t *testing.T) {
	yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
---
apiVersion: v1
kind: Service
metadata:
  name: svc1
`
	docs, err := ParseMultiDoc([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
	if docs[0].GetKind() != "ConfigMap" {
		t.Fatalf("expected first doc kind ConfigMap, got %s", docs[0].GetKind())
	}
	if docs[1].GetKind() != "Service" {
		t.Fatalf("expected second doc kind Service, got %s", docs[1].GetKind())
	}
}

func TestParseMultiDoc_SkipsEmptyDocuments(t *testing.T) {
	yaml := `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
---
---
`
	docs, err := ParseMultiDoc([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document (empty docs skipped), got %d", len(docs))
	}
}

func TestParseMultiDoc_MissingAPIVersion(t *testing.T) {
	yaml := `kind: ConfigMap
metadata:
  name: test
`
	_, err := ParseMultiDoc([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing apiVersion")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseMultiDoc_MissingKind(t *testing.T) {
	yaml := `apiVersion: v1
metadata:
  name: test
`
	_, err := ParseMultiDoc([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing kind")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseMultiDoc_MissingName(t *testing.T) {
	yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  labels:
    app: test
`
	_, err := ParseMultiDoc([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing metadata.name")
	}
	if !strings.Contains(err.Error(), "metadata.name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseMultiDoc_GenerateNameAccepted(t *testing.T) {
	yaml := `apiVersion: v1
kind: Pod
metadata:
  generateName: test-
`
	docs, err := ParseMultiDoc([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
}

func TestParseMultiDoc_JSON(t *testing.T) {
	jsonDoc := `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test"},"data":{"key":"value"}}`
	docs, err := ParseMultiDoc([]byte(jsonDoc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
}

func TestParseMultiDoc_NoValidDocs(t *testing.T) {
	yaml := `---
---
`
	_, err := ParseMultiDoc([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for no valid documents")
	}
	if !strings.Contains(err.Error(), "no valid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Export Tests ---

func TestCleanForExport_StripsServerFields(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":              "test",
				"namespace":         "default",
				"uid":               "abc-123",
				"resourceVersion":   "12345",
				"generation":        int64(1),
				"creationTimestamp":  "2024-01-01T00:00:00Z",
				"managedFields":     []interface{}{},
				"ownerReferences":   []interface{}{},
				"selfLink":          "/api/v1/namespaces/default/configmaps/test",
			},
			"data": map[string]interface{}{
				"key": "value",
			},
			"status": map[string]interface{}{
				"phase": "Active",
			},
		},
	}

	clean := CleanForExport(obj)

	// Check stripped fields
	if _, exists, _ := unstructured.NestedString(clean.Object, "metadata", "uid"); exists {
		t.Error("uid should be stripped")
	}
	if _, exists, _ := unstructured.NestedString(clean.Object, "metadata", "resourceVersion"); exists {
		t.Error("resourceVersion should be stripped")
	}
	if _, exists, _ := unstructured.NestedFieldNoCopy(clean.Object, "metadata", "managedFields"); exists {
		t.Error("managedFields should be stripped")
	}
	if _, exists, _ := unstructured.NestedFieldNoCopy(clean.Object, "metadata", "ownerReferences"); exists {
		t.Error("ownerReferences should be stripped")
	}
	if _, exists, _ := unstructured.NestedFieldNoCopy(clean.Object, "status"); exists {
		t.Error("status should be stripped")
	}

	// Check preserved fields
	if clean.GetName() != "test" {
		t.Errorf("name should be preserved, got %s", clean.GetName())
	}
	if clean.GetNamespace() != "default" {
		t.Errorf("namespace should be preserved, got %s", clean.GetNamespace())
	}

	// Check original is not modified
	if obj.GetName() == "" {
		t.Error("original object should not be modified")
	}
	if _, exists, _ := unstructured.NestedString(obj.Object, "metadata", "uid"); !exists {
		t.Error("original uid should still exist")
	}
}

func TestCleanForExport_StripsNoisyAnnotations(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "test",
				"annotations": map[string]interface{}{
					"kubectl.kubernetes.io/last-applied-configuration": "{}",
					"deployment.kubernetes.io/revision":                "1",
					"app.kubernetes.io/name":                          "test",
				},
			},
		},
	}

	clean := CleanForExport(obj)
	annotations := clean.GetAnnotations()

	if _, ok := annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Error("last-applied-configuration should be stripped")
	}
	if _, ok := annotations["deployment.kubernetes.io/revision"]; ok {
		t.Error("revision annotation should be stripped")
	}
	if annotations["app.kubernetes.io/name"] != "test" {
		t.Error("app.kubernetes.io/name should be preserved")
	}
}

func TestCleanForExport_EmptyAnnotationsRemoved(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "test",
				"annotations": map[string]interface{}{
					"kubectl.kubernetes.io/last-applied-configuration": "{}",
				},
			},
		},
	}

	clean := CleanForExport(obj)
	if _, exists, _ := unstructured.NestedFieldNoCopy(clean.Object, "metadata", "annotations"); exists {
		t.Error("empty annotations map should be removed entirely")
	}
}

func TestExportToYAML_ProducesValidYAML(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":            "test",
				"namespace":       "default",
				"uid":             "abc-123",
				"resourceVersion": "12345",
			},
			"data": map[string]interface{}{
				"key": "value",
			},
		},
	}

	yamlBytes, err := ExportToYAML(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	yamlStr := string(yamlBytes)
	if !strings.Contains(yamlStr, "apiVersion: v1") {
		t.Error("YAML should contain apiVersion")
	}
	if !strings.Contains(yamlStr, "kind: ConfigMap") {
		t.Error("YAML should contain kind")
	}
	if strings.Contains(yamlStr, "uid:") {
		t.Error("YAML should not contain uid")
	}
	if strings.Contains(yamlStr, "resourceVersion:") {
		t.Error("YAML should not contain resourceVersion")
	}
}
