package gitops

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
	Detected    Tool        `json:"detected"`
	ArgoCD      *ToolDetail `json:"argocd,omitempty"`
	FluxCD      *ToolDetail `json:"fluxcd,omitempty"`
	LastChecked string      `json:"lastChecked"`
}

// ToolDetail describes a single GitOps tool's availability.
type ToolDetail struct {
	Available   bool     `json:"available"`
	Namespace   string   `json:"namespace,omitempty"`
	Controllers []string `json:"controllers,omitempty"` // Flux: ["source","kustomize","helm"]
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
	DestinationNamespace string       `json:"destinationNamespace,omitempty"`
	ManagedResourceCount int          `json:"managedResourceCount"`
	Suspended            bool         `json:"suspended"`
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
	App       NormalizedApp   `json:"app"`
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
