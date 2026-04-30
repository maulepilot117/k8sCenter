package externalsecrets

import (
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// computeBaseStatus derives the BASE Status from the Ready condition only.
// Callers layer threshold-aware overlays (Stale, Drifted) via DeriveStatus.
//
// Rules:
//   - Ready=True  → Synced
//   - Ready=False → SyncFailed
//   - Ready=Unknown / no value → Refreshing if condition exists, Unknown if absent
//
// "Conditions entirely absent" maps to Unknown — a brand-new ExternalSecret
// the controller hasn't reconciled yet. Once the controller writes any
// condition the ES is at least Refreshing.
func computeBaseStatus(readyStatus string, hasConditions bool) Status {
	switch readyStatus {
	case "True":
		return StatusSynced
	case "False":
		return StatusSyncFailed
	case "Unknown":
		return StatusRefreshing
	default:
		if hasConditions {
			return StatusRefreshing
		}
		return StatusUnknown
	}
}

// DeriveStatus overlays the threshold-aware states (Stale, Drifted) onto the
// base Status. Precedence:
//
//	SyncFailed > Refreshing > Stale > Drifted > Synced > Unknown
//
// Drift never overrides failure — a Failed cert with detected drift stays
// Failed, because the operator's bigger problem is the failure. Stale wins
// over Drifted because a stale ExternalSecret may not have the latest source
// data, so the drift signal is unreliable.
//
// Stale check requires both a non-zero stale-threshold AND a LastSyncTime —
// without LastSyncTime we have no anchor to compute "older than." A zero
// stale-threshold means the resolver hasn't run yet (Phase A); the function
// silently no-ops the Stale overlay rather than guessing a default.
func DeriveStatus(es ExternalSecret) Status {
	// Failure / refreshing dominate — never overlay Stale or Drifted on top.
	if es.Status == StatusSyncFailed || es.Status == StatusRefreshing {
		return es.Status
	}

	// Stale overlay: only applies when the base is Synced AND we have both
	// a resolved threshold and a last-sync anchor. StaleAfterMinutes is a
	// pointer — nil means the resolver hasn't populated it (Phase A pre-D).
	if es.Status == StatusSynced && es.StaleAfterMinutes != nil && *es.StaleAfterMinutes > 0 && es.LastSyncTime != nil {
		stale := time.Since(*es.LastSyncTime) >= time.Duration(*es.StaleAfterMinutes)*time.Minute
		if stale {
			return StatusStale
		}
	}

	// Drift overlay: only when base is Synced and the live RV check ran.
	if es.Status == StatusSynced && es.DriftStatus == DriftDrifted {
		return StatusDrifted
	}

	return es.Status
}

// normalizeExternalSecret converts an unstructured ESO ExternalSecret into our
// typed shape. The DriftStatus field is left as DriftUnknown — the live
// resourceVersion lookup happens only on the detail endpoint (see Unit 3 of
// the plan; list view explicitly avoids the N+1 impersonated Get).
func normalizeExternalSecret(u *unstructured.Unstructured) ExternalSecret {
	obj := u.Object

	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	storeRef := readStoreRef(spec)
	targetSecret := readTargetSecret(spec)
	refreshInterval := stringFrom(spec, "refreshInterval")

	readyStatus, reason, message := readReadyCondition(status)
	_, hasConditions := status["conditions"].([]any)
	baseStatus := computeBaseStatus(readyStatus, hasConditions)

	syncedRV := stringFrom(status, "syncedResourceVersion")
	lastSync := parseTimeField(status, "refreshTime")

	// Phase D — parse the three threshold annotations off the resource itself.
	// Resolver later walks ES > Store > ClusterStore > default, but the
	// pointer fields here represent ONLY what this ES annotates. The
	// resolver writes back resolved values + per-key sources via
	// ApplyThresholds.
	stalePtr, alertRecPtr, alertLifePtr := readThresholdAnnotations(
		u.GetAnnotations(), u.GetNamespace(), u.GetName(),
	)

	return ExternalSecret{
		Namespace:             u.GetNamespace(),
		Name:                  u.GetName(),
		UID:                   string(u.GetUID()),
		Status:                baseStatus,
		DriftStatus:           DriftUnknown,
		ReadyReason:           reason,
		ReadyMessage:          message,
		StoreRef:              storeRef,
		TargetSecretName:      targetSecret,
		RefreshInterval:       refreshInterval,
		LastSyncTime:          lastSync,
		SyncedResourceVersion: syncedRV,
		StaleAfterMinutes:     stalePtr,
		AlertOnRecovery:       alertRecPtr,
		AlertOnLifecycle:      alertLifePtr,
	}
}

// readThresholdAnnotations extracts the three Phase D annotation values off a
// resource. Returns nil pointers when the annotation is unset or invalid.
// Below-floor values fall through silently — the resolver's slog warning
// path runs from the handler/poller, where the logger is in scope.
func readThresholdAnnotations(annotations map[string]string, namespace, name string) (stale *int, alertRecovery *bool, alertLifecycle *bool) {
	if annotations == nil {
		return nil, nil, nil
	}
	if v, ok := ParseStaleAfterAnnotation(annotations[AnnotationStaleAfterMinutes], namespace, name, nil); ok {
		stale = &v
	}
	if v, ok := ParseBoolAnnotation(annotations[AnnotationAlertOnRecovery]); ok {
		alertRecovery = &v
	}
	if v, ok := ParseBoolAnnotation(annotations[AnnotationAlertOnLifecycle]); ok {
		alertLifecycle = &v
	}
	return
}

// normalizeClusterExternalSecret converts a ClusterExternalSecret. Spec
// embeds an externalSecretSpec — we read storeRef and target from inside it.
func normalizeClusterExternalSecret(u *unstructured.Unstructured) ClusterExternalSecret {
	obj := u.Object

	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	embedded, _ := spec["externalSecretSpec"].(map[string]any)
	if embedded == nil {
		embedded = map[string]any{}
	}

	storeRef := readStoreRef(embedded)
	targetSecret := readTargetSecret(embedded)
	refreshInterval := stringFrom(embedded, "refreshInterval")
	if refreshInterval == "" {
		refreshInterval = stringFrom(spec, "refreshTime")
	}

	readyStatus, reason, message := readReadyCondition(status)
	_, hasConditions := status["conditions"].([]any)

	provisioned := stringSliceFrom(status, "provisionedNamespaces")
	failed := failedNamespacesFrom(status)
	namespaces := stringSliceFrom(spec, "namespaces")
	selectors := namespaceSelectorsFrom(spec)

	return ClusterExternalSecret{
		Name:                   u.GetName(),
		UID:                    string(u.GetUID()),
		Status:                 computeBaseStatus(readyStatus, hasConditions),
		ReadyReason:            reason,
		ReadyMessage:           message,
		StoreRef:               storeRef,
		TargetSecretName:       targetSecret,
		RefreshInterval:        refreshInterval,
		Namespaces:             namespaces,
		NamespaceSelectors:     selectors,
		ProvisionedNamespaces:  provisioned,
		FailedNamespaces:       failed,
		ExternalSecretBaseName: stringFrom(spec, "externalSecretName"),
	}
}

// normalizeSecretStore converts a SecretStore or ClusterSecretStore. scope
// must be "Namespaced" or "Cluster" — there is no way to derive it from the
// unstructured object alone since the kind name lives on the GVR, not the
// payload.
func normalizeSecretStore(u *unstructured.Unstructured, scope string) SecretStore {
	obj := u.Object

	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	provider, providerSpec := detectProvider(spec)

	readyStatus, reason, message := readReadyCondition(status)
	_, hasConditions := status["conditions"].([]any)
	baseStatus := computeBaseStatus(readyStatus, hasConditions)

	ready := readyStatus == "True"
	stalePtr, alertRecPtr, alertLifePtr := readThresholdAnnotations(
		u.GetAnnotations(), u.GetNamespace(), u.GetName(),
	)
	return SecretStore{
		Namespace:         u.GetNamespace(),
		Name:              u.GetName(),
		UID:               string(u.GetUID()),
		Scope:             scope,
		Status:            baseStatus,
		Ready:             ready,
		ReadyReason:       reason,
		ReadyMessage:      message,
		Provider:          provider,
		ProviderSpec:      providerSpec,
		StaleAfterMinutes: stalePtr,
		AlertOnRecovery:   alertRecPtr,
		AlertOnLifecycle:  alertLifePtr,
	}
}

// normalizePushSecret converts a PushSecret. Selector is the source Secret
// name; storeRefs is the list of destinations.
func normalizePushSecret(u *unstructured.Unstructured) PushSecret {
	obj := u.Object

	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	readyStatus, reason, message := readReadyCondition(status)
	_, hasConditions := status["conditions"].([]any)

	storeRefs := readStoreRefList(spec)
	sourceSecret := readPushSourceSecret(spec)
	refreshInterval := stringFrom(spec, "refreshInterval")
	lastSync := parseTimeField(status, "refreshTime")

	return PushSecret{
		Namespace:        u.GetNamespace(),
		Name:             u.GetName(),
		UID:              string(u.GetUID()),
		Status:           computeBaseStatus(readyStatus, hasConditions),
		ReadyReason:      reason,
		ReadyMessage:     message,
		StoreRefs:        storeRefs,
		SourceSecretName: sourceSecret,
		RefreshInterval:  refreshInterval,
		LastSyncTime:     lastSync,
	}
}

// readStoreRef reads spec.secretStoreRef from any ESO spec block (ES, CES.spec.externalSecretSpec).
func readStoreRef(spec map[string]any) StoreRef {
	ref, _ := spec["secretStoreRef"].(map[string]any)
	return StoreRef{
		Name: stringFrom(ref, "name"),
		Kind: stringFrom(ref, "kind"),
	}
}

// readStoreRefList reads spec.secretStoreRefs (PushSecret has many).
func readStoreRefList(spec map[string]any) []StoreRef {
	raw, ok := spec["secretStoreRefs"].([]any)
	if !ok {
		return nil
	}
	out := make([]StoreRef, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, StoreRef{
			Name: stringFrom(m, "name"),
			Kind: stringFrom(m, "kind"),
		})
	}
	return out
}

// readTargetSecret reads the synced-Secret name. ESO normalizes this to
// spec.target.name; if that's missing we fall back to spec.target.template.
// metadata.name (template-driven naming) and finally to status.binding.name.
func readTargetSecret(spec map[string]any) string {
	target, _ := spec["target"].(map[string]any)
	if target == nil {
		return ""
	}
	if name := stringFrom(target, "name"); name != "" {
		return name
	}
	tmpl, _ := target["template"].(map[string]any)
	if tmpl != nil {
		meta, _ := tmpl["metadata"].(map[string]any)
		if meta != nil {
			if name := stringFrom(meta, "name"); name != "" {
				return name
			}
		}
	}
	return ""
}

// readPushSourceSecret reads the source Secret name from a PushSecret's
// spec.selector.secret.name field.
func readPushSourceSecret(spec map[string]any) string {
	sel, _ := spec["selector"].(map[string]any)
	if sel == nil {
		return ""
	}
	secret, _ := sel["secret"].(map[string]any)
	if secret == nil {
		return ""
	}
	return stringFrom(secret, "name")
}

// detectProvider returns the provider key (e.g. "vault") and the
// spec.provider.<provider> sub-object verbatim. Only the first key is
// honored — ESO's schema constrains the provider block to a oneOf, so a
// well-formed object has exactly one key.
func detectProvider(spec map[string]any) (string, map[string]any) {
	provider, _ := spec["provider"].(map[string]any)
	if provider == nil {
		return "", nil
	}
	for k, v := range provider {
		spec, _ := v.(map[string]any)
		return k, spec
	}
	return "", nil
}

// stringSliceFrom safely extracts a top-level string slice from a map.
func stringSliceFrom(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// failedNamespacesFrom reads ClusterExternalSecret status.failedNamespaces,
// which is a list of {namespace, reason} objects. We surface the namespace
// names only — the per-namespace reason can be fetched by drilling into the
// detail page.
func failedNamespacesFrom(status map[string]any) []string {
	raw, ok := status["failedNamespaces"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if ns := stringFrom(m, "namespace"); ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// namespaceSelectorsFrom collapses spec.namespaceSelectors / spec.namespaceSelector
// into a flat string slice for display purposes. We read the selector's
// matchLabels keys so the UI can show "matches: app=production" — full
// LabelSelector reconstruction would require importing apimachinery types we
// don't otherwise need here.
func namespaceSelectorsFrom(spec map[string]any) []string {
	out := []string{}

	// spec.namespaceSelector — single LabelSelector
	if sel, ok := spec["namespaceSelector"].(map[string]any); ok {
		out = append(out, formatLabelSelector(sel)...)
	}

	// spec.namespaceSelectors — list of LabelSelectors (newer syntax)
	if list, ok := spec["namespaceSelectors"].([]any); ok {
		for _, r := range list {
			if sel, ok := r.(map[string]any); ok {
				out = append(out, formatLabelSelector(sel)...)
			}
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// formatLabelSelector renders a LabelSelector's matchLabels as "k=v" strings.
// matchExpressions are compressed to "<key> <op>" (without rendering the value
// list) — full expression rendering is the frontend's job if it wants it; the
// API surface here is for at-a-glance display.
func formatLabelSelector(sel map[string]any) []string {
	out := []string{}
	if labels, ok := sel["matchLabels"].(map[string]any); ok {
		for k, v := range labels {
			if s, ok := v.(string); ok {
				out = append(out, k+"="+s)
			}
		}
	}
	if exprs, ok := sel["matchExpressions"].([]any); ok {
		for _, r := range exprs {
			m, ok := r.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, strings.TrimSpace(stringFrom(m, "key")+" "+stringFrom(m, "operator")))
		}
	}
	return out
}

// readReadyCondition iterates status.conditions looking for type=Ready and
// returns (status, reason, message). Empty strings if not found.
func readReadyCondition(obj map[string]any) (status, reason, message string) {
	conditions, ok := obj["conditions"].([]any)
	if !ok {
		return "", "", ""
	}
	for _, c := range conditions {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cm["type"].(string); t == "Ready" {
			status, _ = cm["status"].(string)
			reason, _ = cm["reason"].(string)
			message, _ = cm["message"].(string)
			return
		}
	}
	return "", "", ""
}

// stringFrom safely extracts a string value from a map.
func stringFrom(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// parseTimeField parses an RFC3339 timestamp from a nested map path. Returns
// nil on any error or missing value.
func parseTimeField(obj map[string]any, fields ...string) *time.Time {
	s, found, err := unstructured.NestedString(obj, fields...)
	if err != nil || !found || s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
