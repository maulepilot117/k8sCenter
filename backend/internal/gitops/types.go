package gitops

import "time"

// Tool identifies which GitOps tool manages a resource.
type Tool string

const (
	ToolNone   Tool = ""
	ToolArgoCD Tool = "argocd"
	ToolFluxCD Tool = "fluxcd"
	ToolBoth   Tool = "both"
)

// SyncStatus is the normalized sync state across Argo CD and Flux CD.
type SyncStatus string

const (
	SyncSynced      SyncStatus = "synced"
	SyncOutOfSync   SyncStatus = "outofsync"
	SyncProgressing SyncStatus = "progressing"
	SyncStalled     SyncStatus = "stalled" // Flux-specific
	SyncFailed      SyncStatus = "failed"
	SyncUnknown     SyncStatus = "unknown"
)

// HealthStatus is the normalized health state across Argo CD and Flux CD.
type HealthStatus string

const (
	HealthHealthy     HealthStatus = "healthy"
	HealthDegraded    HealthStatus = "degraded" // includes Argo "Missing"
	HealthProgressing HealthStatus = "progressing"
	HealthSuspended   HealthStatus = "suspended"
	HealthUnknown     HealthStatus = "unknown"
)

// GitOpsStatus reports which GitOps tools are detected in the cluster.
type GitOpsStatus struct {
	Detected Tool        `json:"detected"`
	ArgoCD   *ToolDetail `json:"argocd,omitempty"`
	FluxCD   *ToolDetail `json:"fluxcd,omitempty"`
	// Reason says why nothing was detected on a remote cluster, from
	// k8s.ReasonCode (discovery_missing, unreachable, ...). Empty when a tool
	// was detected, and always empty for the local cluster.
	Reason      string `json:"reason,omitempty"`
	LastChecked string `json:"lastChecked"`
}

// ToolDetail describes a single GitOps tool's availability.
type ToolDetail struct {
	Available             bool     `json:"available"`
	Namespace             string   `json:"namespace,omitempty"`
	Controllers           []string `json:"controllers,omitempty"` // Flux: ["source","kustomize","helm","notification"]
	AppSetsAvailable      bool     `json:"appSetsAvailable,omitempty"`
	NotificationAvailable bool     `json:"notificationAvailable,omitempty"`
}

// NormalizedApp is the tool-agnostic representation of a GitOps application.
type NormalizedApp struct {
	ID                   string       `json:"id"` // "argo:ns:name" or "flux-ks:ns:name" or "flux-hr:ns:name"
	Name                 string       `json:"name"`
	Namespace            string       `json:"namespace"`
	Tool                 Tool         `json:"tool"`
	Kind                 string       `json:"kind"` // Application, Kustomization, HelmRelease
	SyncStatus           SyncStatus   `json:"syncStatus"`
	HealthStatus         HealthStatus `json:"healthStatus"`
	Source               AppSource    `json:"source"`
	CurrentRevision      string       `json:"currentRevision,omitempty"`
	LastSyncTime         string       `json:"lastSyncTime,omitempty"`
	Message              string       `json:"message,omitempty"`
	DestinationCluster   string       `json:"destinationCluster,omitempty"`
	DestinationName      string       `json:"destinationName,omitempty"` // Argo: spec.destination.name
	DestinationNamespace string       `json:"destinationNamespace,omitempty"`
	// RemoteKubeConfig: a Flux Kustomization with spec.kubeConfig set, which
	// applies to the cluster that kubeconfig names, not the one it runs on.
	RemoteKubeConfig     bool `json:"remoteKubeConfig,omitempty"`
	ManagedResourceCount int  `json:"managedResourceCount"`
	Suspended            bool `json:"suspended"`
}

// AppSource describes where an application's manifests come from.
type AppSource struct {
	RepoURL        string `json:"repoURL,omitempty"`
	Path           string `json:"path,omitempty"`
	TargetRevision string `json:"targetRevision,omitempty"`
	ChartName      string `json:"chartName,omitempty"`
	ChartVersion   string `json:"chartVersion,omitempty"`
}

// ManagedResource is a single resource managed by a GitOps application.
type ManagedResource struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Status    string `json:"status"` // Synced, OutOfSync, etc.
	Health    string `json:"health,omitempty"`
}

// RevisionEntry is a single entry in an application's revision history.
type RevisionEntry struct {
	Revision   string `json:"revision"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	DeployedAt string `json:"deployedAt"`
}

// AppDetail is the full detail response for a single application.
type AppDetail struct {
	App       NormalizedApp     `json:"app"`
	Resources []ManagedResource `json:"resources,omitempty"`
	History   []RevisionEntry   `json:"history,omitempty"`
}

// AppListMetadata provides summary counts for the applications list response.
type AppListMetadata struct {
	Total       int `json:"total"`
	Synced      int `json:"synced"`
	OutOfSync   int `json:"outOfSync"`
	Degraded    int `json:"degraded"`
	Progressing int `json:"progressing"`
	Suspended   int `json:"suspended"`
}

// NormalizedAppSet is the normalized representation of an Argo CD ApplicationSet.
type NormalizedAppSet struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	Namespace           string          `json:"namespace"`
	Tool                Tool            `json:"tool"`
	GeneratorTypes      []string        `json:"generatorTypes"`
	TemplateSource      AppSource       `json:"templateSource"`
	TemplateDestination string          `json:"templateDestination"`
	Status              string          `json:"status"`
	StatusMessage       string          `json:"statusMessage,omitempty"`
	GeneratedAppCount   int             `json:"generatedAppCount"`
	Summary             AppListMetadata `json:"summary"`
	PreserveOnDeletion  bool            `json:"preserveOnDeletion"`
	CreatedAt           string          `json:"createdAt"`
}

// AppSetCondition represents a condition on an ApplicationSet.
type AppSetCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// AppSetDetail is the full detail response for a single ApplicationSet.
type AppSetDetail struct {
	AppSet       NormalizedAppSet  `json:"appSet"`
	Generators   []map[string]any  `json:"generators"`
	Conditions   []AppSetCondition `json:"conditions"`
	Applications []NormalizedApp   `json:"applications"`
}

// OwnershipController names the GitOps controller that claims a live object.
type OwnershipController string

const (
	OwnedByNone   OwnershipController = "none"
	OwnedByArgoCD OwnershipController = "argocd"
	OwnedByFluxCD OwnershipController = "fluxcd"
	OwnedByBoth   OwnershipController = "both"
)

// OwnershipConfidence qualifies how far the evidence actually goes.
type OwnershipConfidence string

const (
	// ConfidenceConfirmed: a controller's own reconciliation record names this
	// exact object. Only EvidenceArgoStatusResource or EvidenceFluxInventoryEntry
	// can produce this value.
	ConfidenceConfirmed OwnershipConfidence = "confirmed"
	// ConfidenceConflicting: two controllers both produced confirming evidence.
	ConfidenceConflicting OwnershipConfidence = "conflicting"
	// ConfidenceUnknown: no confirming evidence. Covers "nothing found at all"
	// (controller none, reason no-evidence) and every search that could not
	// rule ownership out (hints-only, search-bound-exhausted, ...) — the Reason
	// distinguishes them.
	ConfidenceUnknown OwnershipConfidence = "unknown"
	// ConfidenceForbidden: the caller may not list (or get) the controller's
	// applications, so absence of evidence proves nothing.
	ConfidenceForbidden OwnershipConfidence = "forbidden"
	// ConfidenceUnavailable: no controller is installed on the cluster, or a
	// controller's API (or the access check guarding it) errored.
	ConfidenceUnavailable OwnershipConfidence = "unavailable"
)

// OwnershipEvidenceKind classifies one piece of evidence. Only the two
// AUTHORITATIVE kinds may raise confidence to ConfidenceConfirmed (KTD10).
type OwnershipEvidenceKind string

const (
	// Authoritative — the controller's own status.
	EvidenceArgoStatusResource OwnershipEvidenceKind = "argo-status-resource"
	EvidenceFluxInventoryEntry OwnershipEvidenceKind = "flux-inventory-entry"
	// Hints — self-reported by the object, writable by anyone who can write it.
	EvidenceArgoTrackingID OwnershipEvidenceKind = "argo-tracking-annotation"
	EvidenceInstanceLabel  OwnershipEvidenceKind = "instance-label"
	EvidenceManagedByLabel OwnershipEvidenceKind = "managed-by-label"
	EvidenceFieldManager   OwnershipEvidenceKind = "field-manager"
	// EvidenceFluxOwnerLabel is the kustomize.toolkit.fluxcd.io/{name,namespace}
	// or helm.toolkit.fluxcd.io/{name,namespace} label pair Flux stamps on what
	// it applies.
	EvidenceFluxOwnerLabel OwnershipEvidenceKind = "flux-owner-label"
)

// Authoritative reports whether this evidence kind can establish ownership on
// its own. The ONLY place the KTD10 rule is encoded; ResolveOwnership and the
// fuzz oracle both call it.
func (k OwnershipEvidenceKind) Authoritative() bool {
	return k == EvidenceArgoStatusResource || k == EvidenceFluxInventoryEntry
}

// OwnershipEvidence is one observation about one object.
type OwnershipEvidence struct {
	Kind     OwnershipEvidenceKind `json:"kind"`
	Tool     Tool                  `json:"tool"`
	AppID    string                `json:"appId,omitempty"`    // composite id, only when confirmed
	RawValue string                `json:"rawValue,omitempty"` // hint payload, length-capped, never trusted
	Note     string                `json:"note,omitempty"`
}

// ObjectRef identifies a live object for ownership and verification. Version and
// Resource are populated by the apply path (from the RESTMapping); the ownership
// path may leave them empty because neither Argo status.resources[] nor Flux
// inventory entries carry a version. Without both, ResolveOwnership cannot
// read the live object and so gathers no hints for it.
type ObjectRef struct {
	ClusterID string `json:"clusterId"`
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource,omitempty"` // plural, for SelfSubjectAccessReview
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// OwnershipResult is the answer for exactly one ObjectRef.
type OwnershipResult struct {
	Object     ObjectRef           `json:"object"`
	Controller OwnershipController `json:"controller"`
	Confidence OwnershipConfidence `json:"confidence"`
	// Reason is a stable machine code; the full set is the reason* constants
	// in ownership.go.
	Reason string `json:"reason"`
	// Apps carries the confirming applications ONLY. Never populated from hints.
	Apps []OwnedByApp `json:"apps,omitempty"`
	// Evidence lists everything observed, hints included, each labelled.
	Evidence []OwnershipEvidence `json:"evidence,omitempty"`
	// IdentityBasis is always "group-kind-namespace-name" in Release E because
	// neither controller records a UID. UIDConfirmed is therefore always false.
	IdentityBasis string `json:"identityBasis"`
	UIDConfirmed  bool   `json:"uidConfirmed"`
	// WritableGitSource is ALWAYS false in Release E (Q4 unresolved). Present so
	// clients never infer write capability from the presence of Source.
	WritableGitSource bool      `json:"writableGitSource"`
	ObservedAt        time.Time `json:"observedAt"`
}

// OwnedByApp names a confirming application. Source is copied from the
// application the caller is already authorized to list — never from a hint.
type OwnedByApp struct {
	AppID     string    `json:"appId"` // "argo:ns:name" / "flux-ks:ns:name"
	Tool      Tool      `json:"tool"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Source    AppSource `json:"source"`
	Suspended bool      `json:"suspended"`
}
