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
type DriftStatus string

const (
	DriftInSync  DriftStatus = "InSync"
	DriftDrifted DriftStatus = "Drifted"
	DriftUnknown DriftStatus = "Unknown"
)

// Annotation keys honored by the resolver chain (resource > store >
// clusterstore > default). Values must be positive integers (minutes), with a
// 5-minute floor for stale-after to prevent self-DoS against the 60s poller
// cadence. Invalid values are logged and silently fall through to the next
// layer of the chain. Phase D's resolver populates the resolved values on each
// ExternalSecret; Phase A leaves the annotation-resolved fields at zero.
const (
	AnnotationStaleAfterMinutes = "kubecenter.io/eso-stale-after-minutes"
	AnnotationAlertOnRecovery   = "kubecenter.io/eso-alert-on-recovery"
	AnnotationAlertOnLifecycle  = "kubecenter.io/eso-alert-on-lifecycle"
)

// DefaultStaleFallbackMinutes is the package-default stale threshold used when
// the resolver chain produces no value AND the ExternalSecret's
// spec.refreshInterval is unparseable / unset. Matches the 2h fallback in
// requirement R17.
const DefaultStaleFallbackMinutes = 120

// MinStaleAfterMinutes is the floor enforced on the eso-stale-after-minutes
// annotation. Values below this are rejected by the resolver and fall through
// to the next layer of the chain. The floor exists to prevent operators from
// accidentally configuring a threshold tighter than the 60s poller cadence,
// which would mark every ExternalSecret as Stale on every poll.
const MinStaleAfterMinutes = 5

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
// AlertOnLifecycle) are zero in Phase A and populated by Phase D's resolver.
// JSON `omitempty` keeps the response shape clean until then.
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
	// until Phase D's resolver populates them.
	StaleAfterMinutes       int             `json:"staleAfterMinutes,omitempty"`
	StaleAfterMinutesSource ThresholdSource `json:"staleAfterMinutesSource,omitempty"`
	AlertOnRecovery         *bool           `json:"alertOnRecovery,omitempty"`
	AlertOnRecoverySource   ThresholdSource `json:"alertOnRecoverySource,omitempty"`
	AlertOnLifecycle        *bool           `json:"alertOnLifecycle,omitempty"`
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
