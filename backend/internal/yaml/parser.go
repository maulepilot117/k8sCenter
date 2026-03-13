package yaml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

// ParseMultiDoc splits a multi-document YAML (or JSON) stream into individual
// Kubernetes objects. It uses the streaming decoder from k8s.io/apimachinery
// which correctly handles --- document separators per the YAML spec.
//
// Security checks (CheckSecurity) must be called before ParseMultiDoc.
func ParseMultiDoc(data []byte) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured

	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding YAML document %d: %w", len(objects)+1, err)
		}

		// Skip empty documents (e.g., "---\n---")
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || string(trimmed) == "null" {
			continue
		}

		obj := &unstructured.Unstructured{}
		if err := json.Unmarshal(raw, &obj.Object); err != nil {
			return nil, fmt.Errorf("parsing document %d: %w", len(objects)+1, err)
		}

		if obj.Object == nil || len(obj.Object) == 0 {
			continue
		}

		if err := validateRequiredFields(obj, len(objects)+1); err != nil {
			return nil, err
		}

		objects = append(objects, obj)

		if len(objects) >= MaxDocumentCount {
			return nil, fmt.Errorf("too many documents: maximum is %d", MaxDocumentCount)
		}
	}

	if len(objects) == 0 {
		return nil, fmt.Errorf("no valid Kubernetes documents found in YAML")
	}

	// Post-parse expansion ratio check
	jsonSize := 0
	for _, obj := range objects {
		b, _ := json.Marshal(obj.Object)
		jsonSize += len(b)
	}
	if err := CheckExpansionRatio(len(data), jsonSize); err != nil {
		return nil, err
	}

	return objects, nil
}

// validateRequiredFields checks that a parsed document has the minimum required
// Kubernetes fields: apiVersion, kind, and metadata.name.
func validateRequiredFields(obj *unstructured.Unstructured, docIndex int) error {
	if obj.GetAPIVersion() == "" {
		return fmt.Errorf("document %d: apiVersion is required", docIndex)
	}
	if obj.GetKind() == "" {
		return fmt.Errorf("document %d: kind is required", docIndex)
	}
	if obj.GetName() == "" && obj.GetGenerateName() == "" {
		return fmt.Errorf("document %d: metadata.name is required", docIndex)
	}
	return nil
}
