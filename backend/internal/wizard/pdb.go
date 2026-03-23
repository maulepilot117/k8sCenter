package wizard

import (
	"regexp"
	"strconv"
	"strings"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	sigsyaml "sigs.k8s.io/yaml"
)

// pdbValueRegex validates PDB min/max values: non-negative integer or percentage (e.g. "2", "50%").
var pdbValueRegex = regexp.MustCompile(`^(\d+%?)$`)

// PDBInput represents the wizard form data for creating a PodDisruptionBudget.
type PDBInput struct {
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace"`
	Selector       map[string]string `json:"selector"`
	MinAvailable   *string           `json:"minAvailable,omitempty"`
	MaxUnavailable *string           `json:"maxUnavailable,omitempty"`
}

// Validate checks the PDBInput and returns field-level errors.
func (p *PDBInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(p.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if p.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(p.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if len(p.Selector) == 0 {
		errs = append(errs, FieldError{Field: "selector", Message: "at least one label selector is required"})
	} else {
		errs = append(errs, validateLabelMap("selector", p.Selector)...)
	}

	// Exactly one of MinAvailable or MaxUnavailable must be set.
	bothSet := p.MinAvailable != nil && p.MaxUnavailable != nil
	neitherSet := p.MinAvailable == nil && p.MaxUnavailable == nil
	if bothSet || neitherSet {
		errs = append(errs, FieldError{Field: "minAvailable", Message: "exactly one of minAvailable or maxUnavailable must be set"})
	} else {
		// Validate whichever one is set.
		val := p.MinAvailable
		field := "minAvailable"
		if val == nil {
			val = p.MaxUnavailable
			field = "maxUnavailable"
		}
		if err := validatePDBValue(field, *val); err != nil {
			errs = append(errs, *err)
		}
	}

	return errs
}

// validatePDBValue checks that a PDB value is a non-negative integer or a percentage string.
func validatePDBValue(field, value string) *FieldError {
	if !pdbValueRegex.MatchString(value) {
		return &FieldError{Field: field, Message: "must be a non-negative integer or percentage (e.g. \"2\" or \"50%\")"}
	}
	if strings.HasSuffix(value, "%") {
		pct, _ := strconv.Atoi(strings.TrimSuffix(value, "%"))
		if pct > 100 {
			return &FieldError{Field: field, Message: "percentage must be between 0% and 100%"}
		}
	}
	return nil
}

// ToPDB converts the wizard input to a typed Kubernetes PodDisruptionBudget.
// Validate() should be called before ToPDB() to ensure inputs are well-formed.
func (p *PDBInput) ToPDB() *policyv1.PodDisruptionBudget {
	pdb := &policyv1.PodDisruptionBudget{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "policy/v1",
			Kind:       "PodDisruptionBudget",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: p.Selector,
			},
		},
	}

	if p.MinAvailable != nil {
		val := intstr.Parse(*p.MinAvailable)
		pdb.Spec.MinAvailable = &val
	}
	if p.MaxUnavailable != nil {
		val := intstr.Parse(*p.MaxUnavailable)
		pdb.Spec.MaxUnavailable = &val
	}

	return pdb
}

// ToYAML implements WizardInput by converting to a PodDisruptionBudget and marshaling to YAML.
func (p *PDBInput) ToYAML() (string, error) {
	pdb := p.ToPDB()
	yamlBytes, err := sigsyaml.Marshal(pdb)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
