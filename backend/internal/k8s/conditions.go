package k8s

import "strings"

// Condition represents a Kubernetes-style status condition from an unstructured CRD.
type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// ExtractConditions parses a conditions slice from an unstructured object's status.
func ExtractConditions(raw []interface{}) []Condition {
	conditions := make([]Condition, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var c Condition
		if v, ok := m["type"].(string); ok {
			c.Type = v
		}
		if v, ok := m["status"].(string); ok {
			c.Status = v
		}
		if v, ok := m["reason"].(string); ok {
			c.Reason = v
		}
		if v, ok := m["message"].(string); ok {
			c.Message = v
		}
		if v, ok := m["lastTransitionTime"].(string); ok {
			c.LastTransitionTime = v
		}
		conditions = append(conditions, c)
	}
	return conditions
}

// FindCondition returns the first condition matching condType (case-insensitive), or nil.
func FindCondition(conditions []Condition, condType string) *Condition {
	for i := range conditions {
		if strings.EqualFold(conditions[i].Type, condType) {
			return &conditions[i]
		}
	}
	return nil
}

// MapReadyCondition extracts the Ready condition and returns a status string and message.
func MapReadyCondition(conditions []Condition) (string, string) {
	c := FindCondition(conditions, "Ready")
	if c == nil {
		return "Unknown", ""
	}
	switch c.Status {
	case "True":
		return "Ready", c.Message
	case "False":
		return "Not Ready", c.Message
	default:
		return "Unknown", c.Message
	}
}
