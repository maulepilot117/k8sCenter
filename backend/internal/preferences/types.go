// Package preferences serves per-user saved views and resource pins.
//
// The package is deliberately split: this file is pure — it holds the wire
// types, the allowlists, and every validation rule, and imports no net/http —
// so the rules can be tested without a server or a database. handler.go holds
// the HTTP surface.
//
// The allowlists here are the first line of defence and the database's CHECK
// constraints are the second. Anything not named in an allowlist never reaches
// the config column: the validators re-marshal their own typed struct rather
// than passing the caller's bytes through, so an unlisted field cannot survive
// a round trip even if a future decoder stops rejecting it.
package preferences

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

const (
	// SavedViewSchemaVersion and PinSchemaVersion are the only envelope
	// versions this release accepts. A stored record carrying anything else
	// is schema drift and is surfaced to the caller rather than coerced.
	SavedViewSchemaVersion = 1
	PinSchemaVersion       = 1

	// MaxSavedViewsPerUser and MaxPinsPerUser are the per-user ceilings the
	// store enforces inside its INSERT.
	MaxSavedViewsPerUser = 100
	MaxPinsPerUser       = 200

	// Length bounds. Each mirrors a CHECK constraint in migration 000018;
	// validating here turns what would be an opaque constraint violation
	// (a 500) into a deliberate 4xx with a reason code.
	//
	// These count CHARACTERS, not bytes, because the constraints they mirror
	// use char_length. Counting Go's len() here would reject a name in any
	// non-Latin script at a fraction of its stated limit — 128 two-byte
	// characters would fail a "maximum is 128" check at 64 of them.
	maxRecordNameLen = 128 // user_preferences_name_len
	maxOwnerIDLen    = 512 // user_preferences_owner_len
	maxDedupKeyLen   = 512 // user_preferences_dedup_len
	maxSearchLen     = 256
	maxResourceName  = 253 // DNS-1123 subdomain
	maxNamespaceLen  = 63  // DNS-1123 label
	maxUIDLen        = 64

	// maxBodyBytes caps a request body before decoding. The config ceiling in
	// the database is 8 KiB; this leaves room for the envelope around it while
	// still refusing anything a real client would never send.
	maxBodyBytes = 16 << 10
)

var (
	allowedStatusFilters = map[string]struct{}{
		"all": {}, "running": {}, "pending": {}, "failed": {}, "progressing": {},
	}
	// allowedSortKeys mirrors the ResourceTable comparator, which sorts only
	// these three keys. Widening this set requires widening that comparator
	// first, or a saved view would silently sort by name instead.
	allowedSortKeys = map[string]struct{}{"name": {}, "namespace": {}, "age": {}}
	allowedSortDirs = map[string]struct{}{"asc": {}, "desc": {}}
)

// SavedViewConfig is the stored envelope for a saved resource-table view.
type SavedViewConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	ResourceKind  string `json:"resourceKind"`
	Namespace     string `json:"namespace"`
	Search        string `json:"search"`
	StatusFilter  string `json:"statusFilter"`
	SortKey       string `json:"sortKey"`
	SortDir       string `json:"sortDir"`
}

// PinConfig is the stored envelope for a pinned resource.
//
// Group and Version are reserved-empty in this release. They exist so a later
// CRD-pin release can populate them without a schema-version bump; a client
// that sets them today is addressing a contract that does not exist yet, and
// is told so rather than having the values silently dropped.
type PinConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	ResourceKind  string `json:"resourceKind"`
	Group         string `json:"group"`
	Version       string `json:"version"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	UID           string `json:"uid"`
	DisplayKind   string `json:"displayKind"`
}

// CreateRequest is the wire shape for creating a saved view or a pin.
//
// It carries no ownerId and no clusterId on purpose. Both are server-derived —
// the owner from the authenticated session, the cluster from the
// cluster-context middleware — and a request type that cannot express them is
// a stronger guarantee than a handler that remembers to ignore them. The
// decoder additionally rejects unknown fields, so a client that sends either
// one gets a 400 naming the field rather than silently having it dropped.
type CreateRequest struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

// UpdateRequest is the wire shape for updating a saved view. Revision drives
// the optimistic-concurrency check in the store.
type UpdateRequest struct {
	Name     string          `json:"name"`
	Revision int64           `json:"revision"`
	Config   json.RawMessage `json:"config"`
}

// ValidationError carries the machine-readable reason code the handler puts on
// the wire alongside a human-readable message.
type ValidationError struct {
	Reason  string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func invalidf(reason, format string, args ...any) *ValidationError {
	return &ValidationError{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// ValidateSavedView decodes, validates and normalizes a saved-view envelope.
// It returns the typed config and the exact bytes to store — a re-marshal of
// the typed struct, never the caller's input.
func ValidateSavedView(raw json.RawMessage) (SavedViewConfig, json.RawMessage, error) {
	var cfg SavedViewConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, nil, invalidf("invalid_config", "config is not a valid saved-view object")
	}

	if cfg.SchemaVersion != SavedViewSchemaVersion {
		return cfg, nil, invalidf("unsupported_schema_version",
			"unsupported schemaVersion %d; this server accepts %d", cfg.SchemaVersion, SavedViewSchemaVersion)
	}

	adapter := resources.GetAdapter(cfg.ResourceKind)
	if adapter == nil {
		return cfg, nil, invalidf("unknown_resource_kind", "unknown resourceKind %q", cfg.ResourceKind)
	}
	if err := validateScope(adapter.ClusterScoped(), cfg.ResourceKind, cfg.Namespace, false); err != nil {
		return cfg, nil, err
	}

	if _, ok := allowedStatusFilters[cfg.StatusFilter]; !ok {
		return cfg, nil, invalidf("invalid_config", "unsupported statusFilter %q", cfg.StatusFilter)
	}
	if _, ok := allowedSortKeys[cfg.SortKey]; !ok {
		return cfg, nil, invalidf("invalid_config", "unsupported sortKey %q", cfg.SortKey)
	}
	if _, ok := allowedSortDirs[cfg.SortDir]; !ok {
		return cfg, nil, invalidf("invalid_config", "unsupported sortDir %q", cfg.SortDir)
	}

	if n := utf8.RuneCountInString(cfg.Search); n > maxSearchLen {
		return cfg, nil, invalidf("invalid_config",
			"search is %d characters; the maximum is %d", n, maxSearchLen)
	}
	if hasControlChars(cfg.Search) {
		return cfg, nil, invalidf("invalid_config", "search contains control characters")
	}

	normalized, err := json.Marshal(cfg)
	if err != nil {
		return cfg, nil, invalidf("invalid_config", "config could not be normalized")
	}
	return cfg, normalized, nil
}

// ValidatePin decodes, validates and normalizes a pin envelope.
func ValidatePin(raw json.RawMessage) (PinConfig, json.RawMessage, error) {
	var cfg PinConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, nil, invalidf("invalid_config", "config is not a valid pin object")
	}

	if cfg.SchemaVersion != PinSchemaVersion {
		return cfg, nil, invalidf("unsupported_schema_version",
			"unsupported schemaVersion %d; this server accepts %d", cfg.SchemaVersion, PinSchemaVersion)
	}

	adapter := resources.GetAdapter(cfg.ResourceKind)
	if adapter == nil {
		return cfg, nil, invalidf("unknown_resource_kind", "unknown resourceKind %q", cfg.ResourceKind)
	}
	if err := validateScope(adapter.ClusterScoped(), cfg.ResourceKind, cfg.Namespace, true); err != nil {
		return cfg, nil, err
	}

	if cfg.Group != "" || cfg.Version != "" {
		return cfg, nil, invalidf("invalid_config",
			"group and version are reserved and must be empty in this release")
	}

	if cfg.Name == "" || utf8.RuneCountInString(cfg.Name) > maxResourceName || !isDNS1123Subdomain(cfg.Name) {
		return cfg, nil, invalidf("invalid_config", "name %q is not a valid Kubernetes object name", cfg.Name)
	}
	if utf8.RuneCountInString(cfg.UID) > maxUIDLen || !isUIDSafe(cfg.UID) {
		return cfg, nil, invalidf("invalid_config", "uid is not a valid Kubernetes UID")
	}
	if utf8.RuneCountInString(cfg.DisplayKind) > maxRecordNameLen || hasControlChars(cfg.DisplayKind) {
		return cfg, nil, invalidf("invalid_config", "displayKind is not a valid kind label")
	}

	normalized, err := json.Marshal(cfg)
	if err != nil {
		return cfg, nil, invalidf("invalid_config", "config could not be normalized")
	}
	return cfg, normalized, nil
}

// validateScope enforces the namespace rule for a kind. A cluster-scoped kind
// must carry no namespace. A namespaced kind may carry none for a saved view
// (that is the "all namespaces" scope) but must carry one for a pin, which
// addresses exactly one object.
func validateScope(clusterScoped bool, kind, namespace string, namespaceRequired bool) error {
	if clusterScoped {
		if namespace != "" {
			return invalidf("invalid_config",
				"resourceKind %q is cluster-scoped and cannot carry a namespace", kind)
		}
		return nil
	}
	if namespace == "" {
		if namespaceRequired {
			return invalidf("invalid_config", "resourceKind %q requires a namespace", kind)
		}
		return nil
	}
	if utf8.RuneCountInString(namespace) > maxNamespaceLen || !isDNS1123Label(namespace) {
		return invalidf("invalid_config", "namespace %q is not a valid DNS-1123 label", namespace)
	}
	return nil
}

// ValidateRecordName checks the user-supplied label a record is listed under.
func ValidateRecordName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return invalidf("invalid_name", "name is required")
	}
	if n := utf8.RuneCountInString(name); n > maxRecordNameLen {
		return invalidf("invalid_name",
			"name is %d characters; the maximum is %d", n, maxRecordNameLen)
	}
	if hasControlChars(name) {
		return invalidf("invalid_name", "name contains control characters")
	}
	return nil
}

// ValidateOwnerID checks the server-supplied identity against the column it
// has to fit in.
//
// Unlike every other bound here, the caller does not control this value: it is
// auth.User.ID, which for an LDAP identity is a full distinguished name. An
// identity longer than the column would otherwise reach the database and come
// back as a constraint violation — a 500 that tells the user nothing and the
// operator almost as little. Refusing it here produces a deliberate response
// naming the real cause, which an operator can act on by shortening the
// mapped identity attribute.
func ValidateOwnerID(ownerID string) error {
	if n := utf8.RuneCountInString(ownerID); ownerID == "" || n > maxOwnerIDLen {
		return invalidf("identity_too_long",
			"the authenticated identity is %d characters; preferences support at most %d",
			n, maxOwnerIDLen)
	}
	return nil
}

// ValidateDedupKey guards the derived identity column. Every key this package
// builds is well under the bound — a saved view's is the folded name, a pin's
// is kind/namespace/name — so this is a backstop against a future derivation
// that grows, not a limit a client can reach directly.
func ValidateDedupKey(key string) error {
	if key == "" || utf8.RuneCountInString(key) > maxDedupKeyLen {
		return invalidf("invalid_config", "record identity is not addressable")
	}
	return nil
}

// SavedViewDedupKey folds a saved view's display name to its identity, so
// "Prod Pods" and "prod pods" are the same view rather than two.
func SavedViewDedupKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// PinDedupKey builds a pin's identity from the resource it addresses.
//
// The uid is deliberately excluded. A deleted-and-recreated object keeps its
// kind, namespace and name but gets a fresh uid; including the uid would make
// that a second, separate pin pointing at the same place, and the user would
// see a duplicate they never created. Excluding it makes the recreation
// collide with the existing pin, which is what lets the UI report the target
// as replaced (R1, R6). The uid rides along inside the config as evidence of
// which object was pinned.
func PinDedupKey(c PinConfig) string {
	return c.ResourceKind + "/" + c.Namespace + "/" + c.Name
}

func hasControlChars(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// isDNS1123Label reports whether s is a valid DNS-1123 label: lowercase
// alphanumerics and '-', starting and ending alphanumeric.
func isDNS1123Label(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			continue
		case r == '-':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isDNS1123Subdomain reports whether s is a valid DNS-1123 subdomain: dot-
// separated DNS-1123 labels.
func isDNS1123Subdomain(s string) bool {
	if s == "" {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !isDNS1123Label(label) {
			return false
		}
	}
	return true
}

// isUIDSafe reports whether s is limited to the characters a Kubernetes UID
// uses. An empty uid is allowed: it is evidence, not identity, and a client
// that has not resolved one yet may still pin the object.
func isUIDSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}
