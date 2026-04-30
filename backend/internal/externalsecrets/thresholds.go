package externalsecrets

import (
	"log/slog"
	"strconv"
	"time"
)

// Annotation keys honored on ExternalSecret, SecretStore, and ClusterSecretStore.
// The same keys are read at every level of the resolution chain — the resolver
// just picks the strongest layer that supplied a valid value.
const (
	// AnnotationStaleAfterMinutes is the maximum gap between sync attempts
	// before an otherwise-Synced ExternalSecret is overlaid as Stale.
	// Positive integer; values below MinStaleAfterMinutes are rejected.
	AnnotationStaleAfterMinutes = "kubecenter.io/eso-stale-after-minutes"

	// AnnotationAlertOnRecovery toggles "recovered" notifications for the
	// resource. Boolean ("true"/"false"); invalid values fall through.
	AnnotationAlertOnRecovery = "kubecenter.io/eso-alert-on-recovery"

	// AnnotationAlertOnLifecycle toggles created/deleted/first_synced events.
	// Off by default; only emits when explicitly set true at any layer.
	AnnotationAlertOnLifecycle = "kubecenter.io/eso-alert-on-lifecycle"
)

// Resolver constants. Defaults match the plan §Phase D Unit 12 contract:
//   - Stale fallback when refreshInterval is unset: 2h.
//   - Stale floor: 5 minutes (defends the 60s poller against self-DoS via an
//     aggressive operator override).
//   - AlertOnRecovery default: false. Operators opt in.
//   - AlertOnLifecycle default: false. Operators opt in.
const (
	// DefaultStaleAfterMinutesFallback is the absolute fallback when
	// refreshInterval is missing or unparseable. Plan: 2h.
	DefaultStaleAfterMinutesFallback = 120

	// MinStaleAfterMinutes is the floor; annotations below this are rejected
	// and the chain continues to the next layer (or eventually the default).
	MinStaleAfterMinutes = 5

	// DefaultStaleAfterMultiplier is applied to the parsed refreshInterval.
	// Plan: 2 × refreshInterval.
	DefaultStaleAfterMultiplier = 2

	// DefaultAlertOnRecovery: opt-in via annotation.
	DefaultAlertOnRecovery = false

	// DefaultAlertOnLifecycle: opt-in via annotation.
	DefaultAlertOnLifecycle = false
)

// ParseStaleAfterAnnotation parses a "kubecenter.io/eso-stale-after-minutes"
// value. Returns (value, true) when the input is a positive integer at or above
// MinStaleAfterMinutes; (0, false) for any other input including missing,
// non-numeric, zero, negative, and below-floor values. Below-floor values are
// rejected with a logged warning so operators can find aggressive overrides.
func ParseStaleAfterAnnotation(raw string, ctx, name string, logger *slog.Logger) (int, bool) {
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n < MinStaleAfterMinutes {
		if logger != nil {
			logger.Warn("externalsecrets: stale-after annotation below 5-minute floor — falling through",
				"context", ctx,
				"name", name,
				"value", n,
			)
		}
		return 0, false
	}
	return n, true
}

// ParseBoolAnnotation parses "true"/"false" (case-insensitive). Returns
// (value, true) on success; (false, false) when missing or unparseable.
func ParseBoolAnnotation(raw string) (bool, bool) {
	if raw == "" {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return v, true
}

// computeStaleDefault returns the package-default stale-after value for an ES.
// Plan: 2 × refreshInterval, fallback DefaultStaleAfterMinutesFallback when
// refreshInterval is unset or unparseable. If the doubled value would still
// fall under MinStaleAfterMinutes (e.g., refreshInterval=1m), clamps to the
// floor — never returns a value the resolver itself would reject.
func computeStaleDefault(refreshInterval string) int {
	if refreshInterval == "" {
		return DefaultStaleAfterMinutesFallback
	}
	d, err := time.ParseDuration(refreshInterval)
	if err != nil || d <= 0 {
		return DefaultStaleAfterMinutesFallback
	}
	mins := int(d.Minutes()) * DefaultStaleAfterMultiplier
	if mins < MinStaleAfterMinutes {
		return MinStaleAfterMinutes
	}
	return mins
}

// resolveIntKey walks the ES > Store > ClusterStore chain for a single int
// threshold key. extract picks the right pointer field off a SecretStore so
// the same chain logic services every key.
//
// storesByNSName is keyed by "<namespace>/<name>" (SecretStore is namespaced —
// stores with the same name in different namespaces must stay distinct).
// clusterStoresByName is keyed by name only (ClusterSecretStore is unique
// cluster-wide).
func resolveIntKey(
	esValue *int,
	esNamespace string,
	ref StoreRef,
	storesByNSName map[string]SecretStore,
	clusterStoresByName map[string]SecretStore,
	defaultValue int,
	extract func(SecretStore) *int,
) (int, ThresholdSource) {
	if esValue != nil && *esValue > 0 {
		return *esValue, ThresholdSourceExternalSecret
	}

	switch ref.Kind {
	case "SecretStore":
		if s, ok := storesByNSName[esNamespace+"/"+ref.Name]; ok {
			if v := extract(s); v != nil && *v > 0 {
				return *v, ThresholdSourceSecretStore
			}
		}
	case "ClusterSecretStore":
		if s, ok := clusterStoresByName[ref.Name]; ok {
			if v := extract(s); v != nil && *v > 0 {
				return *v, ThresholdSourceClusterSecretStore
			}
		}
	}

	return defaultValue, ThresholdSourceDefault
}

// resolveBoolKey is the bool equivalent of resolveIntKey. The "set" predicate
// for bool is "pointer non-nil" rather than "value > 0" — false is a legal
// operator-set value (e.g., "stop alerting on recovery for this ES").
func resolveBoolKey(
	esValue *bool,
	esNamespace string,
	ref StoreRef,
	storesByNSName map[string]SecretStore,
	clusterStoresByName map[string]SecretStore,
	defaultValue bool,
	extract func(SecretStore) *bool,
) (bool, ThresholdSource) {
	if esValue != nil {
		return *esValue, ThresholdSourceExternalSecret
	}

	switch ref.Kind {
	case "SecretStore":
		if s, ok := storesByNSName[esNamespace+"/"+ref.Name]; ok {
			if v := extract(s); v != nil {
				return *v, ThresholdSourceSecretStore
			}
		}
	case "ClusterSecretStore":
		if s, ok := clusterStoresByName[ref.Name]; ok {
			if v := extract(s); v != nil {
				return *v, ThresholdSourceClusterSecretStore
			}
		}
	}

	return defaultValue, ThresholdSourceDefault
}

// ResolveESOThresholds walks the ES > Store > ClusterStore > default chain for
// each threshold key independently and returns the resolved values plus
// per-key sources. Each key resolves on its own — an ES that sets stale-after
// alone picks up stale-after from the ES annotation and alert-on-recovery
// from the next layer that has it.
//
// Unlike the cert-manager equivalent there is no warn-vs-crit ordering check;
// stale/recovery/lifecycle have no inter-key constraint, so no thresholdConflict
// path exists.
//
// storesByNSName / clusterStoresByName: see ApplyThresholds. Pass nil when no
// stores exist; the resolver falls through to defaults uniformly.
func ResolveESOThresholds(
	es ExternalSecret,
	storesByNSName map[string]SecretStore,
	clusterStoresByName map[string]SecretStore,
	logger *slog.Logger,
) (
	stale int,
	staleSource ThresholdSource,
	alertRecovery bool,
	alertRecoverySource ThresholdSource,
	alertLifecycle bool,
	alertLifecycleSource ThresholdSource,
) {
	stale, staleSource = resolveIntKey(
		es.StaleAfterMinutes,
		es.Namespace,
		es.StoreRef,
		storesByNSName,
		clusterStoresByName,
		computeStaleDefault(es.RefreshInterval),
		func(s SecretStore) *int { return s.StaleAfterMinutes },
	)
	alertRecovery, alertRecoverySource = resolveBoolKey(
		es.AlertOnRecovery,
		es.Namespace,
		es.StoreRef,
		storesByNSName,
		clusterStoresByName,
		DefaultAlertOnRecovery,
		func(s SecretStore) *bool { return s.AlertOnRecovery },
	)
	alertLifecycle, alertLifecycleSource = resolveBoolKey(
		es.AlertOnLifecycle,
		es.Namespace,
		es.StoreRef,
		storesByNSName,
		clusterStoresByName,
		DefaultAlertOnLifecycle,
		func(s SecretStore) *bool { return s.AlertOnLifecycle },
	)
	return
}

// ApplyThresholds resolves and writes effective thresholds onto every
// ExternalSecret in the slice, then re-derives the final Status (so the
// stale overlay can fire). The slice is mutated IN PLACE — mirrors
// internal/certmanager.ApplyThresholds and the standard Go in-place mutator
// convention (sort.Sort, slices.SortFunc).
//
// Index maps are built once up front (O(ess) instead of O(ess × stores)).
// Pass nil for the store slices when none exist — the resolver falls through
// to defaults uniformly.
func ApplyThresholds(
	ess []ExternalSecret,
	stores []SecretStore,
	clusterStores []SecretStore,
	logger *slog.Logger,
) {
	if len(ess) == 0 {
		return
	}

	storesByNSName := indexStoresByNamespacedName(stores)
	clusterStoresByName := indexStoresByName(clusterStores)

	for i := range ess {
		stale, staleSource, alertRec, alertRecSource, alertLife, alertLifeSource := ResolveESOThresholds(
			ess[i], storesByNSName, clusterStoresByName, logger,
		)

		// Write resolved values back as pointers so the wire shape carries
		// the resolved value rather than nil. Existing pointer fields are
		// reused — re-pointing is fine, the original raw-annotation slot
		// has already informed the resolver.
		ess[i].StaleAfterMinutes = &stale
		ess[i].StaleAfterMinutesSource = sanitizeSource(staleSource)

		alertRecCopy := alertRec
		ess[i].AlertOnRecovery = &alertRecCopy
		ess[i].AlertOnRecoverySource = sanitizeSource(alertRecSource)

		alertLifeCopy := alertLife
		ess[i].AlertOnLifecycle = &alertLifeCopy
		ess[i].AlertOnLifecycleSource = sanitizeSource(alertLifeSource)

		ess[i].Status = DeriveStatus(ess[i])
	}
}

// sanitizeSource forces an unknown ThresholdSource value back to
// ThresholdSourceDefault. Belt-and-suspenders against a future Go-side bug
// emitting an out-of-enum string that would break the frontend's exhaustive
// switch. Mirrors internal/certmanager.sanitizeSource.
func sanitizeSource(s ThresholdSource) ThresholdSource {
	if s.Valid() {
		return s
	}
	return ThresholdSourceDefault
}

// indexStoresByNamespacedName builds a "<namespace>/<name>" -> SecretStore
// map. Used for namespace-scoped SecretStores so two stores named "vault" in
// different namespaces stay distinct.
func indexStoresByNamespacedName(stores []SecretStore) map[string]SecretStore {
	m := make(map[string]SecretStore, len(stores))
	for _, s := range stores {
		m[s.Namespace+"/"+s.Name] = s
	}
	return m
}

// indexStoresByName builds a name -> SecretStore map. Used for
// ClusterSecretStores, which are cluster-scoped and uniquely named.
func indexStoresByName(stores []SecretStore) map[string]SecretStore {
	m := make(map[string]SecretStore, len(stores))
	for _, s := range stores {
		m[s.Name] = s
	}
	return m
}
