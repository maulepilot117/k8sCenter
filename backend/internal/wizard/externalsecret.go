package wizard

import (
	"fmt"
	"strings"
	"time"

	sigsyaml "sigs.k8s.io/yaml"
)

// ExternalSecretStoreRefInput identifies the SecretStore or ClusterSecretStore
// the ExternalSecret reads from.
type ExternalSecretStoreRefInput struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "SecretStore" or "ClusterSecretStore"
}

// ExternalSecretRemoteRefInput is the source-store coordinate for a single
// data entry. Property and Version are optional and only emitted when set.
type ExternalSecretRemoteRefInput struct {
	Key      string `json:"key"`
	Property string `json:"property,omitempty"`
	Version  string `json:"version,omitempty"`
}

// ExternalSecretDataItemInput maps one source-store ref to a key in the
// target Kubernetes Secret.
type ExternalSecretDataItemInput struct {
	SecretKey string                       `json:"secretKey"`
	RemoteRef ExternalSecretRemoteRefInput `json:"remoteRef"`
}

// ExternalSecretDataFromInput pulls every key from a single remote path
// into the target Secret. Either Extract or Find may be set; Extract takes
// the verbatim remote object and copies its keys, Find performs a name
// match against the source store.
type ExternalSecretDataFromInput struct {
	Extract *ExternalSecretRemoteRefInput `json:"extract,omitempty"`
	Find    *ExternalSecretFindInput      `json:"find,omitempty"`
}

// ExternalSecretFindInput is the v1 dataFrom.find shape. Only Name.RegExp is
// supported in the wizard surface; tags and path are deferred to the YAML
// editor (most users want a single regex match).
type ExternalSecretFindInput struct {
	Path string                 `json:"path,omitempty"`
	Name *ExternalSecretFindBy  `json:"name,omitempty"`
	Tags map[string]string      `json:"tags,omitempty"`
}

// ExternalSecretFindBy holds a single regex used by dataFrom.find.name.
type ExternalSecretFindBy struct {
	RegExp string `json:"regexp"`
}

// ExternalSecretInput represents the wizard form data for creating an
// external-secrets.io/v1 ExternalSecret.
type ExternalSecretInput struct {
	Name             string                        `json:"name"`
	Namespace        string                        `json:"namespace"`
	StoreRef         ExternalSecretStoreRefInput   `json:"storeRef"`
	RefreshInterval  string                        `json:"refreshInterval,omitempty"`
	TargetSecretName string                        `json:"targetSecretName,omitempty"`
	Data             []ExternalSecretDataItemInput `json:"data,omitempty"`
	DataFrom         []ExternalSecretDataFromInput `json:"dataFrom,omitempty"`
}

// Validate checks the ExternalSecretInput and returns field-level errors.
func (e *ExternalSecretInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(e.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if e.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(e.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	// storeRef
	if e.StoreRef.Kind != "SecretStore" && e.StoreRef.Kind != "ClusterSecretStore" {
		errs = append(errs, FieldError{Field: "storeRef.kind", Message: "must be SecretStore or ClusterSecretStore"})
	}
	if e.StoreRef.Name == "" {
		errs = append(errs, FieldError{Field: "storeRef.name", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(e.StoreRef.Name) {
		errs = append(errs, FieldError{Field: "storeRef.name", Message: "must be a valid DNS label"})
	}

	// targetSecretName: required so the wizard always renders a deterministic
	// target Secret name in the preview (ESO defaults to ExternalSecret.name
	// when omitted, but exposing the default explicitly avoids surprise).
	if e.TargetSecretName == "" {
		errs = append(errs, FieldError{Field: "targetSecretName", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(e.TargetSecretName) {
		errs = append(errs, FieldError{Field: "targetSecretName", Message: "must be a valid DNS label"})
	}

	// refreshInterval — optional; when set must parse as a non-negative Go
	// duration. ESO accepts "0" to disable polling, so we allow zero.
	if e.RefreshInterval != "" {
		d, err := time.ParseDuration(e.RefreshInterval)
		if err != nil {
			errs = append(errs, FieldError{Field: "refreshInterval", Message: "must be a valid Go duration (e.g. 1h)"})
		} else if d < 0 {
			errs = append(errs, FieldError{Field: "refreshInterval", Message: "must not be negative"})
		}
	}

	// At least one of data or dataFrom is required.
	if len(e.Data) == 0 && len(e.DataFrom) == 0 {
		errs = append(errs, FieldError{Field: "data", Message: "must specify at least one of data or dataFrom"})
	}

	// Per-item data validation.
	seenSecretKeys := map[string]bool{}
	for i, item := range e.Data {
		if item.SecretKey == "" {
			errs = append(errs, FieldError{Field: fmt.Sprintf("data[%d].secretKey", i), Message: "is required"})
		} else if seenSecretKeys[item.SecretKey] {
			errs = append(errs, FieldError{Field: fmt.Sprintf("data[%d].secretKey", i), Message: "duplicate secretKey"})
		} else {
			seenSecretKeys[item.SecretKey] = true
		}
		if strings.TrimSpace(item.RemoteRef.Key) == "" {
			errs = append(errs, FieldError{Field: fmt.Sprintf("data[%d].remoteRef.key", i), Message: "is required"})
		}
	}

	// Per-item dataFrom validation. Each entry must specify exactly one of
	// extract or find. Find regex must compile.
	for i, df := range e.DataFrom {
		hasExtract := df.Extract != nil
		hasFind := df.Find != nil
		switch {
		case !hasExtract && !hasFind:
			errs = append(errs, FieldError{Field: fmt.Sprintf("dataFrom[%d]", i), Message: "must specify either extract or find"})
		case hasExtract && hasFind:
			errs = append(errs, FieldError{Field: fmt.Sprintf("dataFrom[%d]", i), Message: "must specify only one of extract or find"})
		case hasExtract:
			if strings.TrimSpace(df.Extract.Key) == "" {
				errs = append(errs, FieldError{Field: fmt.Sprintf("dataFrom[%d].extract.key", i), Message: "is required"})
			}
		case hasFind:
			if df.Find.Name == nil || strings.TrimSpace(df.Find.Name.RegExp) == "" {
				errs = append(errs, FieldError{Field: fmt.Sprintf("dataFrom[%d].find.name.regexp", i), Message: "is required"})
			}
		}
	}

	return errs
}

// ToExternalSecret returns a map representation suitable for YAML marshaling.
// Built as map[string]any (per L7.1) so the wizard does not pull the ESO Go
// SDK into go.mod just for type-checking the spec.
func (e *ExternalSecretInput) ToExternalSecret() map[string]any {
	storeRef := map[string]any{
		"name": e.StoreRef.Name,
		"kind": e.StoreRef.Kind,
	}

	target := map[string]any{
		"name": e.TargetSecretName,
	}

	spec := map[string]any{
		"secretStoreRef": storeRef,
		"target":         target,
	}
	if e.RefreshInterval != "" {
		spec["refreshInterval"] = e.RefreshInterval
	}

	if len(e.Data) > 0 {
		data := make([]map[string]any, 0, len(e.Data))
		for _, item := range e.Data {
			ref := map[string]any{"key": item.RemoteRef.Key}
			if item.RemoteRef.Property != "" {
				ref["property"] = item.RemoteRef.Property
			}
			if item.RemoteRef.Version != "" {
				ref["version"] = item.RemoteRef.Version
			}
			data = append(data, map[string]any{
				"secretKey": item.SecretKey,
				"remoteRef": ref,
			})
		}
		spec["data"] = data
	}

	if len(e.DataFrom) > 0 {
		dataFrom := make([]map[string]any, 0, len(e.DataFrom))
		for _, df := range e.DataFrom {
			entry := map[string]any{}
			if df.Extract != nil {
				ex := map[string]any{"key": df.Extract.Key}
				if df.Extract.Property != "" {
					ex["property"] = df.Extract.Property
				}
				if df.Extract.Version != "" {
					ex["version"] = df.Extract.Version
				}
				entry["extract"] = ex
			}
			if df.Find != nil {
				find := map[string]any{}
				if df.Find.Path != "" {
					find["path"] = df.Find.Path
				}
				if df.Find.Name != nil {
					find["name"] = map[string]any{"regexp": df.Find.Name.RegExp}
				}
				if len(df.Find.Tags) > 0 {
					tags := map[string]any{}
					for k, v := range df.Find.Tags {
						tags[k] = v
					}
					find["tags"] = tags
				}
				entry["find"] = find
			}
			dataFrom = append(dataFrom, entry)
		}
		spec["dataFrom"] = dataFrom
	}

	return map[string]any{
		"apiVersion": "external-secrets.io/v1",
		"kind":       "ExternalSecret",
		"metadata": map[string]any{
			"name":      e.Name,
			"namespace": e.Namespace,
		},
		"spec": spec,
	}
}

// ToYAML implements WizardInput.
func (e *ExternalSecretInput) ToYAML() (string, error) {
	y, err := sigsyaml.Marshal(e.ToExternalSecret())
	if err != nil {
		return "", err
	}
	return string(y), nil
}
