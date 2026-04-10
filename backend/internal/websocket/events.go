package websocket

import "sync"

// ResourceEvent is emitted by informer event handlers and consumed by the Hub.
type ResourceEvent struct {
	EventType string `json:"eventType"` // ADDED, MODIFIED, DELETED
	Kind      string `json:"kind"`      // deployments, pods, etc.
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Object    any    `json:"object"` // full k8s object (same shape as REST response)
}

// subKey identifies a subscription topic (resource kind + namespace).
type subKey struct {
	Kind      string
	Namespace string // empty = all namespaces
}

// Message types for the WebSocket wire protocol.
const (
	MsgTypeAuth          = "auth"
	MsgTypeAuthOK        = "auth_ok"
	MsgTypeSubscribe     = "subscribe"
	MsgTypeUnsubscribe   = "unsubscribe"
	MsgTypeSubscribed    = "subscribed"
	MsgTypeEvent         = "event"
	MsgTypeError         = "error"
	MsgTypeResyncRequired = "resync_required"
)

// IncomingMessage is the envelope for client-to-server messages.
type IncomingMessage struct {
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`     // auth
	ID        string `json:"id,omitempty"`        // subscribe/unsubscribe
	Kind      string `json:"kind,omitempty"`      // subscribe
	Namespace string `json:"namespace,omitempty"` // subscribe
}

// OutgoingMessage is the envelope for server-to-client messages.
type OutgoingMessage struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	EventType string `json:"eventType,omitempty"` // ADDED/MODIFIED/DELETED
	Code      int    `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	Object    any    `json:"object,omitempty"`
}

// allowedKinds is the set of resource kinds that clients may subscribe to via WebSocket.
// Secrets are intentionally excluded — they are not in the informer cache and must only
// be accessed via the REST API with masking and audit logging.
// Protected by allowedKindsMu for concurrent access from discovery goroutines.
var (
	allowedKindsMu sync.RWMutex
	allowedKinds   = map[string]bool{
		"notifications":           true,
		"pods":                    true,
		"services":                true,
		"configmaps":              true,
		"namespaces":              true,
		"nodes":                   true,
		"persistentvolumeclaims":  true,
		"pvcs":                    true, // alias — normalized to persistentvolumeclaims
		"persistentvolumes":       true,
		"pvs":                     true, // alias — normalized to persistentvolumes
		"endpoints":               true,
		"events":                  true,
		"deployments":             true,
		"replicasets":             true,
		"statefulsets":            true,
		"daemonsets":              true,
		"jobs":                    true,
		"cronjobs":                true,
		"ingresses":              true,
		"networkpolicies":         true,
		"horizontalpodautoscalers": true,
		"hpas":                    true, // alias — normalized to horizontalpodautoscalers
		"storageclasses":          true,
		"roles":                   true,
		"clusterroles":            true,
		"rolebindings":            true,
		"clusterrolebindings":     true,
		"resourcequotas":          true,
		"limitranges":             true,
		"serviceaccounts":         true,
		"poddisruptionbudgets":    true,
		"pdbs":                    true, // alias — normalized to poddisruptionbudgets
		"endpointslices":          true,
		"alerts":                  true,
		"validatingwebhookconfigurations": true,
		"mutatingwebhookconfigurations":   true,
	}
	// crdKindGroups maps CRD kind strings to their API group for RBAC checks.
	// Populated dynamically by RegisterAllowedKind when apiGroup is non-empty.
	crdKindGroups = map[string]string{}
)

// RegisterAllowedKind dynamically adds a kind to the subscription allowlist.
// If apiGroup is non-empty, the kind is treated as a CRD and RBAC checks will
// use group-aware access checking.
func RegisterAllowedKind(kind, apiGroup string) {
	allowedKindsMu.Lock()
	defer allowedKindsMu.Unlock()
	if allowedKinds[kind] {
		return // already registered
	}
	allowedKinds[kind] = true
	if apiGroup != "" {
		crdKindGroups[kind] = apiGroup
	}
}

// UnregisterAllowedKind removes a kind from the subscription allowlist.
func UnregisterAllowedKind(kind string) {
	allowedKindsMu.Lock()
	defer allowedKindsMu.Unlock()
	delete(allowedKinds, kind)
	delete(crdKindGroups, kind)
}

// isAllowedKind returns true if the kind is in the subscription allowlist.
func isAllowedKind(kind string) bool {
	allowedKindsMu.RLock()
	defer allowedKindsMu.RUnlock()
	return allowedKinds[kind]
}

// crdAPIGroup returns the API group for a CRD kind, or empty string for core resources.
func crdAPIGroup(kind string) string {
	allowedKindsMu.RLock()
	defer allowedKindsMu.RUnlock()
	return crdKindGroups[kind]
}

// kindAliases maps frontend short names to the informer's canonical kind strings.
var kindAliases = map[string]string{
	"pvcs": "persistentvolumeclaims",
	"pvs":  "persistentvolumes",
	"hpas": "horizontalpodautoscalers",
	"pdbs": "poddisruptionbudgets",
}

// normalizeKind maps alias kind strings to their canonical form used by informers.
func normalizeKind(kind string) string {
	if canonical, ok := kindAliases[kind]; ok {
		return canonical
	}
	return kind
}
