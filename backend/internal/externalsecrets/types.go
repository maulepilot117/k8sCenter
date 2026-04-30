// Package externalsecrets provides External Secrets Operator (ESO) integration for k8sCenter.
//
// Mirrors the cert-manager package layout 1:1 (discovery → normalize → cached
// handler with singleflight + RBAC). Surfaces the five v1 CRDs of the ESO API
// as observatory endpoints under /api/v1/externalsecrets/*. Write actions
// (force-sync, bulk refresh) are deferred to Phase E; persistence and drift
// detection ship in Phase C; alerting + annotation thresholds ship in Phase D.
package externalsecrets

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the API group ESO publishes its v1 CRDs under. Exported for
// RBAC checks (CanAccessGroupResource) and the Helm chart's ClusterRole grant.
const GroupName = "external-secrets.io"

// GVR constants for external-secrets.io/v1 resources. v1 was promoted to GA in
// ESO v0.14 (mid-2025); v1beta1 is served-but-not-stored and is handled
// transparently by the dynamic client.
var (
	ExternalSecretGVR = schema.GroupVersionResource{
		Group: GroupName, Version: "v1", Resource: "externalsecrets",
	}
	ClusterExternalSecretGVR = schema.GroupVersionResource{
		Group: GroupName, Version: "v1", Resource: "clusterexternalsecrets",
	}
	SecretStoreGVR = schema.GroupVersionResource{
		Group: GroupName, Version: "v1", Resource: "secretstores",
	}
	ClusterSecretStoreGVR = schema.GroupVersionResource{
		Group: GroupName, Version: "v1", Resource: "clustersecretstores",
	}
	PushSecretGVR = schema.GroupVersionResource{
		Group: GroupName, Version: "v1", Resource: "pushsecrets",
	}
)

// Status represents the computed lifecycle state of an ExternalSecret.
type Status string

const (
	StatusSynced     Status = "Synced"
	StatusSyncFailed Status = "SyncFailed"
	StatusRefreshing Status = "Refreshing"
	StatusStale      Status = "Stale"
	StatusDrifted    Status = "Drifted"
	StatusUnknown    Status = "Unknown"
)

// DriftStatus is a tri-state indicator of whether an ExternalSecret's synced
// Secret matches the resource version it was last reconciled against. The
// distinction between Drifted (operator-edited the Secret) and Unknown (the
// provider doesn't populate syncedResourceVersion, so we cannot tell) is
// surfaced to operators rather than collapsed into one boolean.
//
// Wire contract: the LIST endpoint OMITS this field entirely (drift
// resolution requires a per-ES impersonated `get secret` which would N+1
// the list). Callers must treat the absence of `driftStatus` on a list
// item as DriftUnknown — never as DriftInSync. The DETAIL endpoint always
// populates it. Phase C's Unit 11 will add a coarse
// `lastObservedDriftStatus` to the list response sourced from the
// poller's persisted history; the contract above still holds (absence ==
// Unknown) for the detail-vs-list split.
type DriftStatus string

const (
	DriftInSync  DriftStatus = "InSync"
	DriftDrifted DriftStatus = "Drifted"
	DriftUnknown DriftStatus = "Unknown"
)

// Annotation keys, default thresholds, and the floor for stale-after-minutes
// are owned by Phase D's `thresholds.go`. Phase A keeps the annotation-resolved
// fields zero/nil and ships no resolver. The constants live alongside the
// resolver they parameterize so the package surface here stays minimal.

// ThresholdSource enumerates which layer of the resolution chain supplied an
// annotation-resolved value. Used by the UI to surface "Stale at: 60m
// (Store apps/vault-store)" rather than collapsing all sources into a single
// "annotation set" boolean.
type ThresholdSource string

const (
	ThresholdSourceDefault            ThresholdSource = "default"
	ThresholdSourceExternalSecret     ThresholdSource = "externalsecret"
	ThresholdSourceSecretStore        ThresholdSource = "secretstore"
	ThresholdSourceClusterSecretStore ThresholdSource = "clustersecretstore"
)

// Valid reports whether s is one of the four enum constants. Used at write
// sites as a belt-and-suspenders guard so a future Go-side bug cannot emit an
// out-of-enum string that would break the frontend's exhaustive switch.
func (s ThresholdSource) Valid() bool {
	switch s {
	case ThresholdSourceDefault,
		ThresholdSourceExternalSecret,
		ThresholdSourceSecretStore,
		ThresholdSourceClusterSecretStore:
		return true
	}
	return false
}

// ESOStatus is returned by GET /externalsecrets/status.
type ESOStatus struct {
	Detected    bool      `json:"detected"`
	Namespace   string    `json:"namespace,omitempty"`
	Version     string    `json:"version,omitempty"`
	LastChecked time.Time `json:"lastChecked"`
}

// StoreRef identifies the SecretStore or ClusterSecretStore that an
// ExternalSecret reads from.
type StoreRef struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "SecretStore" or "ClusterSecretStore"
}

// ExternalSecret is the API representation of an external-secrets.io/v1
// ExternalSecret resource.
//
// The annotation-resolved fields (StaleAfterMinutes, AlertOnRecovery,
// AlertOnLifecycle, plus the *Source sibling fields) are nil/empty in
// Phase A and populated by Phase D's resolver. JSON `omitempty` keeps the
// response shape clean until then.
type ExternalSecret struct {
	Namespace             string      `json:"namespace"`
	Name                  string      `json:"name"`
	UID                   string      `json:"uid"`
	Status                Status      `json:"status"`
	DriftStatus           DriftStatus `json:"driftStatus,omitempty"`
	ReadyReason           string      `json:"readyReason,omitempty"`
	ReadyMessage          string      `json:"readyMessage,omitempty"`
	StoreRef              StoreRef    `json:"storeRef"`
	TargetSecretName      string      `json:"targetSecretName,omitempty"`
	RefreshInterval       string      `json:"refreshInterval,omitempty"` // e.g. "1h"
	LastSyncTime          *time.Time  `json:"lastSyncTime,omitempty"`
	SyncedResourceVersion string      `json:"syncedResourceVersion,omitempty"`

	// Phase D — annotation-resolved threshold fields. omitempty hides them
	// until Phase D's resolver populates them. Each value carries a
	// ThresholdSource sibling so the UI can attribute "Stale at 60m
	// (Store apps/vault-store)" rather than collapsing all sources into one
	// boolean. StaleAfterMinutes is *int (rather than int) so the wire shape
	// distinguishes "resolver hasn't run / Phase A" (omitted) from
	// "resolver ran, no value resolved" (zero) — matches SecretStore's
	// nullable shape and lets DeriveStatus key on presence rather than
	// truthiness.
	StaleAfterMinutes       *int            `json:"staleAfterMinutes,omitempty"`
	StaleAfterMinutesSource ThresholdSource `json:"staleAfterMinutesSource,omitempty"`
	AlertOnRecovery         *bool           `json:"alertOnRecovery,omitempty"`
	AlertOnRecoverySource   ThresholdSource `json:"alertOnRecoverySource,omitempty"`
	AlertOnLifecycle        *bool           `json:"alertOnLifecycle,omitempty"`
	AlertOnLifecycleSource  ThresholdSource `json:"alertOnLifecycleSource,omitempty"`

	// DriftUnknownReason disambiguates a DriftStatus=Unknown response. Empty
	// when DriftStatus is not Unknown. Allowed values: `no_synced_rv`,
	// `no_target_name`, `secret_deleted`, `rbac_denied`, `transient_error`,
	// `client_error`, `secret_not_owned`. Frontend renders this as a
	// hover-tooltip under the drift indicator so an operator can see WHY
	// drift wasn't resolvable rather than guessing.
	DriftUnknownReason string `json:"driftUnknownReason,omitempty"`

	// LastObservedDriftStatus is the poller's last-observed drift state
	// for this ES. The list endpoint populates this from the handler's
	// in-memory map (refreshed every 60s by the poller). The detail
	// endpoint instead resolves DriftStatus live via an impersonated
	// `get secret` and leaves this field empty. Operators reading the
	// list view see a stale-by-up-to-90s drift hint (60s poller +
	// 30s handler cache); the detail page is the source of truth.
	LastObservedDriftStatus DriftStatus `json:"lastObservedDriftStatus,omitempty"`
}

// ClusterExternalSecret is the API representation of a ClusterExternalSecret —
// the cluster-scoped form that fans an ExternalSecret out across multiple
// namespaces selected by NamespaceSelector / Namespaces fields.
type ClusterExternalSecret struct {
	Name                   string   `json:"name"`
	UID                    string   `json:"uid"`
	Status                 Status   `json:"status"`
	ReadyReason            string   `json:"readyReason,omitempty"`
	ReadyMessage           string   `json:"readyMessage,omitempty"`
	StoreRef               StoreRef `json:"storeRef"`
	TargetSecretName       string   `json:"targetSecretName,omitempty"`
	RefreshInterval        string   `json:"refreshInterval,omitempty"`
	NamespaceSelectors     []string `json:"namespaceSelectors,omitempty"`
	Namespaces             []string `json:"namespaces,omitempty"`
	ProvisionedNamespaces  []string `json:"provisionedNamespaces,omitempty"`
	FailedNamespaces       []string `json:"failedNamespaces,omitempty"`
	ExternalSecretBaseName string   `json:"externalSecretBaseName,omitempty"`
}

// SecretStore is the API representation of a SecretStore or ClusterSecretStore.
// Scope distinguishes the two: "Namespaced" or "Cluster".
//
// Provider names the source-store family ("vault", "aws", "gcp", etc.) so the
// frontend can render provider-specific affordances. ProviderSpec carries the
// raw spec.provider.<provider> sub-object verbatim (map[string]any, never
// typed) — per L7.1 we deliberately avoid pulling per-provider Go SDKs into
// go.mod just for type-checking the spec.
type SecretStore struct {
	Namespace    string         `json:"namespace,omitempty"` // empty for ClusterSecretStore
	Name         string         `json:"name"`
	UID          string         `json:"uid"`
	Scope        string         `json:"scope"` // "Namespaced" or "Cluster"
	Status       Status         `json:"status"`
	Ready        bool           `json:"ready"`
	ReadyReason  string         `json:"readyReason,omitempty"`
	ReadyMessage string         `json:"readyMessage,omitempty"`
	Provider     string         `json:"provider"`
	ProviderSpec map[string]any `json:"providerSpec,omitempty"`

	// Phase D — annotation-resolved threshold fields. Pointer types so the
	// resolver can distinguish "store didn't set this" (nil) from "store
	// set it to zero" (which would be invalid and rejected anyway, but the
	// nullability still matters for chain inheritance).
	StaleAfterMinutes *int  `json:"staleAfterMinutes,omitempty"`
	AlertOnRecovery   *bool `json:"alertOnRecovery,omitempty"`
	AlertOnLifecycle  *bool `json:"alertOnLifecycle,omitempty"`
}

// PushSecret is the API representation of a PushSecret — the inverse-direction
// CRD that pushes a Kubernetes Secret out to a source store. Read-only in v1
// per the requirements doc; spec is intentionally surfaced (cluster admin
// controls who has list pushsecrets via RBAC).
type PushSecret struct {
	Namespace        string     `json:"namespace"`
	Name             string     `json:"name"`
	UID              string     `json:"uid"`
	Status           Status     `json:"status"`
	ReadyReason      string     `json:"readyReason,omitempty"`
	ReadyMessage     string     `json:"readyMessage,omitempty"`
	StoreRefs        []StoreRef `json:"storeRefs"`
	SourceSecretName string     `json:"sourceSecretName,omitempty"`
	RefreshInterval  string     `json:"refreshInterval,omitempty"`
	LastSyncTime     *time.Time `json:"lastSyncTime,omitempty"`
}
