package notification

import (
	"regexp"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// FluxNotificationGroup is the API group of every Flux notification resource.
const FluxNotificationGroup = "notification.toolkit.fluxcd.io"

var (
	// FluxProviderGVR is the GVR for Flux Notification Provider resources.
	FluxProviderGVR = schema.GroupVersionResource{
		Group:    FluxNotificationGroup,
		Version:  "v1beta3",
		Resource: "providers",
	}
	// FluxAlertGVR is the GVR for Flux Notification Alert resources.
	FluxAlertGVR = schema.GroupVersionResource{
		Group:    FluxNotificationGroup,
		Version:  "v1beta3",
		Resource: "alerts",
	}
	// FluxReceiverGVR is the GVR for Flux Notification Receiver resources.
	FluxReceiverGVR = schema.GroupVersionResource{
		Group:    FluxNotificationGroup,
		Version:  "v1",
		Resource: "receivers",
	}
)

// remoteVersions lists, per resource, the versions this package reads and
// writes on a remote cluster, most preferred first; the first a cluster
// serves is used (#534). The local cluster always uses the GVRs above.
//
// Providers and Alerts fall back to v1beta2 (Flux 2.0) because every field
// this package reads or writes has the same name and shape there: v1beta3
// removed the status subresource and deprecated spec.interval and
// spec.summary, none of which a body built here sets. A v1beta2 object still
// carries Ready conditions, which the normalizers map like any other. Two
// limits apply to the fallback: it counts only on Flux 2.0 or later (see
// remoteServed), and Flux 2.0 accepts fewer Provider types
// (v1beta2ProviderTypes).
var remoteVersions = map[string][]string{
	FluxProviderGVR.Resource: {FluxProviderGVR.Version, v1beta2},
	FluxAlertGVR.Resource:    {FluxAlertGVR.Version, v1beta2},
	FluxReceiverGVR.Resource: {FluxReceiverGVR.Version},
}

// v1beta2 is the Provider and Alert version Flux 2.0 serves.
const v1beta2 = "v1beta2"

// v1beta2ProviderTypes is the spec.type enum of the Flux 2.0
// (notification-controller v1.0.0) v1beta2 Provider CRD. Later types are
// refused before a write at v1beta2 rather than left to the cluster's
// generic 422.
var v1beta2ProviderTypes = map[string]bool{
	"slack": true, "discord": true, "msteams": true, "rocket": true,
	"generic": true, "generic-hmac": true, "github": true, "gitlab": true,
	"gitea": true, "bitbucket": true, "azuredevops": true, "googlechat": true,
	"googlepubsub": true, "webex": true, "sentry": true, "azureeventhub": true,
	"telegram": true, "lark": true, "matrix": true, "opsgenie": true,
	"alertmanager": true, "grafana": true, "githubdispatch": true, "pagerduty": true,
}

// providerTypeServedAt reports whether a Provider of providerType can be
// written at gvr's version.
func providerTypeServedAt(gvr schema.GroupVersionResource, providerType string) bool {
	return gvr.Version != v1beta2 || v1beta2ProviderTypes[providerType]
}

const managedByLabel = "app.kubernetes.io/managed-by"
const managedByValue = "kubecenter"

var k8sNameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)

// validProviderTypes lists all supported Flux Notification Provider types.
var validProviderTypes = map[string]bool{
	"slack": true, "discord": true, "msteams": true, "googlechat": true,
	"rocket": true, "webex": true, "telegram": true, "lark": true,
	"matrix": true, "zulip": true,
	"github": true, "gitlab": true, "gitea": true, "bitbucket": true,
	"bitbucketserver": true, "azuredevops": true,
	"githubpullrequestcomment": true, "gitlabmergerequestcomment": true,
	"giteapullrequestcomment": true,
	"githubdispatch":          true,
	"grafana":                 true, "alertmanager": true, "sentry": true,
	"pagerduty": true, "opsgenie": true, "datadog": true, "otel": true,
	"googlepubsub": true, "azureeventhub": true, "nats": true,
	"generic": true, "generic-hmac": true,
}

// validReceiverTypes lists all supported Flux Notification Receiver types.
var validReceiverTypes = map[string]bool{
	"generic": true, "generic-hmac": true,
	"github": true, "gitlab": true, "bitbucket": true,
	"harbor": true, "dockerhub": true, "quay": true,
	"gcr": true, "nexus": true, "acr": true, "cdevents": true,
}

// validEventSeverities lists the Flux-supported event severity values.
var validEventSeverities = map[string]bool{
	"info": true, "error": true,
}

// validEventSourceKinds lists the Flux CRD kinds that can appear as event sources.
var validEventSourceKinds = map[string]bool{
	"Kustomization":         true,
	"HelmRelease":           true,
	"GitRepository":         true,
	"OCIRepository":         true,
	"Bucket":                true,
	"HelmRepository":        true,
	"HelmChart":             true,
	"ImageRepository":       true,
	"ImagePolicy":           true,
	"ImageUpdateAutomation": true,
}

// NormalizedProvider is the normalized representation of a Flux Notification Provider.
type NormalizedProvider struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"` // "slack", "discord", "github", "generic", etc.
	Channel   string `json:"channel"`
	Address   string `json:"address"`   // may be empty if stored in secret
	SecretRef string `json:"secretRef"` // secret name (never the value)
	Suspend   bool   `json:"suspend"`
	Status    string `json:"status"`  // "Ready", "Not Ready", "Suspended"
	Message   string `json:"message"` // condition message
	CreatedAt string `json:"createdAt"`
}

// NormalizedAlert is the normalized representation of a Flux Notification Alert.
type NormalizedAlert struct {
	Name          string           `json:"name"`
	Namespace     string           `json:"namespace"`
	ProviderRef   string           `json:"providerRef"`   // provider name in same namespace
	EventSeverity string           `json:"eventSeverity"` // "info" or "error"
	EventSources  []EventSourceRef `json:"eventSources"`
	InclusionList []string         `json:"inclusionList"`
	ExclusionList []string         `json:"exclusionList"`
	Suspend       bool             `json:"suspend"`
	Status        string           `json:"status"`
	Message       string           `json:"message"`
	CreatedAt     string           `json:"createdAt"`
}

// EventSourceRef identifies a Flux resource that generates events.
type EventSourceRef struct {
	Kind        string            `json:"kind"`                // Kustomization, HelmRelease, GitRepository, etc.
	Name        string            `json:"name"`                // specific name or "*"
	Namespace   string            `json:"namespace,omitempty"` // empty = same as Alert
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

// NormalizedReceiver is the normalized representation of a Flux Notification Receiver.
type NormalizedReceiver struct {
	Name        string           `json:"name"`
	Namespace   string           `json:"namespace"`
	Type        string           `json:"type"`      // "github", "gitlab", "generic", etc.
	Resources   []EventSourceRef `json:"resources"` // resources to reconcile
	SecretRef   string           `json:"secretRef"`
	Suspend     bool             `json:"suspend"`
	WebhookPath string           `json:"webhookPath"` // from status
	Status      string           `json:"status"`
	Message     string           `json:"message"`
	CreatedAt   string           `json:"createdAt"`
}

// NotificationStatus reports availability and counts for Flux Notification resources.
type NotificationStatus struct {
	Available     bool `json:"available"`
	ProviderCount int  `json:"providerCount"`
	AlertCount    int  `json:"alertCount"`
	ReceiverCount int  `json:"receiverCount"`
	// Reason is set on a remote cluster when Available is false: why the
	// notification API is not usable there (discovery_missing, unreachable,
	// ...). Absent on the local cluster.
	Reason string `json:"reason,omitempty"`
	// Coverage names each remote list that could not be read, so a count of
	// zero is not mistaken for none.
	Coverage []k8s.SourceCoverage `json:"coverage,omitempty"`
}
