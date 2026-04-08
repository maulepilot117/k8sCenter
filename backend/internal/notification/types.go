package notification

import (
	"regexp"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// FluxProviderGVR is the GVR for Flux Notification Provider resources.
	FluxProviderGVR = schema.GroupVersionResource{
		Group:    "notification.toolkit.fluxcd.io",
		Version:  "v1beta3",
		Resource: "providers",
	}
	// FluxAlertGVR is the GVR for Flux Notification Alert resources.
	FluxAlertGVR = schema.GroupVersionResource{
		Group:    "notification.toolkit.fluxcd.io",
		Version:  "v1beta3",
		Resource: "alerts",
	}
	// FluxReceiverGVR is the GVR for Flux Notification Receiver resources.
	FluxReceiverGVR = schema.GroupVersionResource{
		Group:    "notification.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "receivers",
	}
)

const managedByLabel = "app.kubernetes.io/managed-by"
const managedByValue = "kubecenter"

var k8sNameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)

// validProviderTypes lists all supported Flux Notification Provider types.
var validProviderTypes = map[string]bool{
	"slack": true, "discord": true, "msteams": true, "googlechat": true,
	"rocket": true, "webex": true, "telegram": true, "lark": true,
	"matrix": true, "zulip": true,
	"github": true, "gitlab": true, "gitea": true, "bitbucket": true,
	"bitbucketserver": true, "azuredevops": true,
	"githubpullrequestcomment": true, "gitlabmergerequestcomment": true,
	"giteapullrequestcomment": true,
	"githubdispatch": true,
	"grafana": true, "alertmanager": true, "sentry": true,
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

// NormalizedProvider is the normalized representation of a Flux Notification Provider.
type NormalizedProvider struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"`      // "slack", "discord", "github", "generic", etc.
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
	Kind        string            `json:"kind"`                  // Kustomization, HelmRelease, GitRepository, etc.
	Name        string            `json:"name"`                  // specific name or "*"
	Namespace   string            `json:"namespace,omitempty"`   // empty = same as Alert
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

// NormalizedReceiver is the normalized representation of a Flux Notification Receiver.
type NormalizedReceiver struct {
	Name        string           `json:"name"`
	Namespace   string           `json:"namespace"`
	Type        string           `json:"type"`      // "github", "gitlab", "generic", etc.
	Resources   []EventSourceRef `json:"resources"` // resources to reconcile
	WebhookPath string           `json:"webhookPath"` // from status
	SecretRef   string           `json:"secretRef"`
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
}
