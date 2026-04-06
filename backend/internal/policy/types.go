package policy

// Engine identifies which policy engine manages a resource.
type Engine string

const (
	EngineNone       Engine = ""
	EngineKyverno    Engine = "kyverno"
	EngineGatekeeper Engine = "gatekeeper"
	EngineBoth       Engine = "both"
)

// defaultSeverity is used when a policy does not declare severity.
const defaultSeverity = "medium"

// severityWeights maps severity labels to relative weights for compliance scoring.
var severityWeights = map[string]int{
	"critical": 10,
	"high":     5,
	"medium":   2,
	"low":      1,
}

// EngineStatus reports the detected policy engines in the cluster.
type EngineStatus struct {
	Detected    Engine        `json:"detected"`
	Kyverno     *EngineDetail `json:"kyverno,omitempty"`
	Gatekeeper  *EngineDetail `json:"gatekeeper,omitempty"`
	LastChecked string        `json:"lastChecked"`
}

// EngineDetail describes a single policy engine's availability.
type EngineDetail struct {
	Available bool   `json:"available"`
	Namespace string `json:"namespace,omitempty"`
	Webhooks  int    `json:"webhooks"`
}

// NormalizedPolicy is the engine-agnostic representation of a policy.
type NormalizedPolicy struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Namespace      string   `json:"namespace,omitempty"`
	Kind           string   `json:"kind"`
	Action         string   `json:"action"`
	Category       string   `json:"category,omitempty"`
	Severity       string   `json:"severity"`
	Description    string   `json:"description,omitempty"`
	NativeAction   string   `json:"nativeAction"`
	Engine         Engine   `json:"engine"`
	Blocking       bool     `json:"blocking"`
	Ready          bool     `json:"ready"`
	RuleCount      int      `json:"ruleCount"`
	ViolationCount int      `json:"violationCount"`
	TargetKinds    []string `json:"targetKinds,omitempty"`
}

// NormalizedViolation is the engine-agnostic representation of a policy violation.
type NormalizedViolation struct {
	Policy    string `json:"policy"`
	Rule      string `json:"rule,omitempty"`
	Severity  string `json:"severity"`
	Action    string `json:"action"`
	Message   string `json:"message"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Timestamp string `json:"timestamp,omitempty"`
	Engine    Engine `json:"engine"`
	Blocking  bool   `json:"blocking"`
}

// ComplianceScore holds weighted pass/fail metrics for a scope.
type ComplianceScore struct {
	Scope      string                    `json:"scope"`
	Score      float64                   `json:"score"`
	Pass       int                       `json:"pass"`
	Fail       int                       `json:"fail"`
	Warn       int                       `json:"warn"`
	Total      int                       `json:"total"`
	BySeverity map[string]SeverityCounts `json:"bySeverity,omitempty"`
}

// SeverityCounts holds pass/fail breakdown for a single severity level.
type SeverityCounts struct {
	Pass  int `json:"pass"`
	Fail  int `json:"fail"`
	Total int `json:"total"`
}
