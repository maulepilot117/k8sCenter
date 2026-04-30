package certmanager

import (
	"log/slog"
)

// effectiveWarn returns the cert's resolved warning threshold, falling
// back to the package default when the field is zero (i.e. when
// ApplyThresholds hasn't been called yet, or the cert was constructed
// in a test fixture that bypassed it). Centralizing this fallback
// keeps DeriveStatus, the poller bucket function, and the /expiring
// filter from drifting out of step on the zero-value semantics.
func effectiveWarn(cert Certificate) int {
	if cert.WarningThresholdDays > 0 {
		return cert.WarningThresholdDays
	}
	return WarningThresholdDays
}

// effectiveCrit is the critical-threshold equivalent of effectiveWarn.
func effectiveCrit(cert Certificate) int {
	if cert.CriticalThresholdDays > 0 {
		return cert.CriticalThresholdDays
	}
	return CriticalThresholdDays
}

// ResolveCertThresholds walks the cert > issuer > clusterissuer >
// default chain for each threshold key independently and returns the
// effective (warn, crit, aggregate source, conflict). Each key
// resolves on its own — a cert that sets warn but not crit picks up
// warn from the cert annotation and crit from the next layer down.
//
// The aggregate source identifies the strongest layer that contributed
// a value (precedence: certificate > issuer > clusterissuer > default).
// Per-key sources are surfaced separately via ApplyThresholds onto the
// Certificate so the frontend can render "Warns at 60d (from Issuer X),
// critical at 14d (Default)" instead of misattributing the whole pair
// to the strongest layer.
//
// If the resolved warn/crit pair would violate crit < warn (e.g., cert
// sets warn=14 and issuer sets crit=20 — each valid alone, conflicting
// in combination), the resolver falls back to package defaults AND
// returns conflict=true so the UI can surface "Threshold conflict —
// using defaults" rather than misleading "Default" with no signal.
// The slog warning identifies which annotation values produced the
// conflict so operators can find and fix the override.
//
// issuersByNSName / clusterIssuersByName: see indexIssuers* helpers.
// Pass nil for either when none exist; resolver falls through to
// defaults uniformly.
func ResolveCertThresholds(
	cert Certificate,
	issuersByNSName map[string]Issuer,
	clusterIssuersByName map[string]Issuer,
	logger *slog.Logger,
) (warn, crit int, source ThresholdSource, conflict bool) {
	warn, warnSource := resolveThresholdKey(
		cert.WarningThresholdDays,
		cert.Namespace,
		cert.IssuerRef,
		issuersByNSName,
		clusterIssuersByName,
		WarningThresholdDays,
		func(i Issuer) *int { return i.WarningThresholdDays },
	)
	crit, critSource := resolveThresholdKey(
		cert.CriticalThresholdDays,
		cert.Namespace,
		cert.IssuerRef,
		issuersByNSName,
		clusterIssuersByName,
		CriticalThresholdDays,
		func(i Issuer) *int { return i.CriticalThresholdDays },
	)

	// Sanity-check: crit must be strictly less than warn.
	if crit >= warn {
		if logger != nil {
			logger.Warn("certmanager: resolved thresholds violate crit < warn; falling back to defaults",
				"namespace", cert.Namespace,
				"name", cert.Name,
				"resolvedWarn", warn,
				"resolvedCrit", crit,
				"warnSource", warnSource,
				"critSource", critSource,
			)
		}
		return WarningThresholdDays, CriticalThresholdDays, ThresholdSourceDefault, true
	}

	return warn, crit, strongerSource(warnSource, critSource), false
}

// ResolveCertThresholdsDetailed is the variant used by ApplyThresholds
// to populate per-key source fields on the Certificate. It does the
// same chain walk as ResolveCertThresholds but exposes the per-key
// sources separately. Defined alongside (rather than expanding the
// already-busy public signature) so external callers that don't care
// about per-key attribution can stick with the simpler API.
func resolveCertThresholdsDetailed(
	cert Certificate,
	issuersByNSName map[string]Issuer,
	clusterIssuersByName map[string]Issuer,
	logger *slog.Logger,
) (warn, crit int, warnSource, critSource ThresholdSource, conflict bool) {
	warn, warnSource = resolveThresholdKey(
		cert.WarningThresholdDays,
		cert.Namespace,
		cert.IssuerRef,
		issuersByNSName,
		clusterIssuersByName,
		WarningThresholdDays,
		func(i Issuer) *int { return i.WarningThresholdDays },
	)
	crit, critSource = resolveThresholdKey(
		cert.CriticalThresholdDays,
		cert.Namespace,
		cert.IssuerRef,
		issuersByNSName,
		clusterIssuersByName,
		CriticalThresholdDays,
		func(i Issuer) *int { return i.CriticalThresholdDays },
	)
	if crit >= warn {
		if logger != nil {
			logger.Warn("certmanager: resolved thresholds violate crit < warn; falling back to defaults",
				"namespace", cert.Namespace,
				"name", cert.Name,
				"resolvedWarn", warn,
				"resolvedCrit", crit,
				"warnSource", warnSource,
				"critSource", critSource,
			)
		}
		return WarningThresholdDays, CriticalThresholdDays, ThresholdSourceDefault, ThresholdSourceDefault, true
	}
	return warn, crit, warnSource, critSource, false
}

// resolveThresholdKey walks one threshold (warn or crit) through the
// resolution chain. extract picks the right pointer field off an Issuer
// so the same chain logic services both keys.
//
// issuersByNSName is keyed by "<namespace>/<name>" to avoid collisions
// between Issuers of the same name in different namespaces (Issuers
// are namespace-scoped). clusterIssuersByName is keyed by name only
// (ClusterIssuers are cluster-scoped and unique).
func resolveThresholdKey(
	certValue int,
	certNamespace string,
	ref IssuerRef,
	issuersByNSName map[string]Issuer,
	clusterIssuersByName map[string]Issuer,
	defaultValue int,
	extract func(Issuer) *int,
) (int, ThresholdSource) {
	// Cert-level annotation. normalizeCertificate only stores positive
	// values, so a non-zero field always means "the operator set this".
	if certValue > 0 {
		return certValue, ThresholdSourceCertificate
	}

	// Issuer / ClusterIssuer level — pick the right map based on the
	// referenced kind so a Certificate with kind=Issuer can't fall
	// through to a same-named ClusterIssuer (or vice versa) and silently
	// pick up the wrong override.
	switch ref.Kind {
	case "Issuer":
		if iss, ok := issuersByNSName[certNamespace+"/"+ref.Name]; ok {
			if v := extract(iss); v != nil && *v > 0 {
				return *v, ThresholdSourceIssuer
			}
		}
	case "ClusterIssuer":
		if iss, ok := clusterIssuersByName[ref.Name]; ok {
			if v := extract(iss); v != nil && *v > 0 {
				return *v, ThresholdSourceClusterIssuer
			}
		}
	}

	return defaultValue, ThresholdSourceDefault
}

// strongerSource returns the stronger of two ThresholdSource values,
// where strength is certificate > issuer > clusterissuer > default.
// Used to aggregate the per-key sources into a single field on the
// Certificate response.
func strongerSource(a, b ThresholdSource) ThresholdSource {
	if sourceRank(a) >= sourceRank(b) {
		return a
	}
	return b
}

func sourceRank(s ThresholdSource) int {
	switch s {
	case ThresholdSourceCertificate:
		return 3
	case ThresholdSourceIssuer:
		return 2
	case ThresholdSourceClusterIssuer:
		return 1
	default:
		return 0
	}
}

// ApplyThresholds resolves and writes effective thresholds onto every
// Certificate in the slice, then derives the final Status. The slice
// is mutated IN PLACE — callers see their existing slice updated;
// there is no return value, mirroring the standard Go convention for
// in-place mutators (sort.Sort, slices.SortFunc).
//
// Issuer lookups are built once up front (O(certs) instead of
// O(certs * issuers)). Pass nil for the issuer slices when none exist
// — the resolver falls through to package defaults uniformly.
func ApplyThresholds(certs []Certificate, issuers, clusterIssuers []Issuer, logger *slog.Logger) {
	if len(certs) == 0 {
		return
	}

	issuersByNSName := indexIssuersByNamespacedName(issuers)
	clusterIssuersByName := indexByName(clusterIssuers)

	for i := range certs {
		warn, crit, warnSource, critSource, conflict := resolveCertThresholdsDetailed(
			certs[i], issuersByNSName, clusterIssuersByName, logger,
		)
		certs[i].WarningThresholdDays = warn
		certs[i].CriticalThresholdDays = crit
		certs[i].WarningThresholdSource = sanitizeSource(warnSource)
		certs[i].CriticalThresholdSource = sanitizeSource(critSource)
		certs[i].ThresholdSource = sanitizeSource(strongerSource(warnSource, critSource))
		certs[i].ThresholdConflict = conflict
		certs[i].Status = DeriveStatus(certs[i])
	}
}

// sanitizeSource is a belt-and-suspenders guard that forces an unknown
// ThresholdSource value back to ThresholdSourceDefault. The internal
// resolver paths only ever produce one of the four legal values, but
// a future refactor that synthesizes a string outside the enum would
// silently break the frontend's switch — sanitize at the write site
// so the wire contract holds.
func sanitizeSource(s ThresholdSource) ThresholdSource {
	if s.Valid() {
		return s
	}
	return ThresholdSourceDefault
}

// indexIssuersByNamespacedName builds a "<namespace>/<name>" -> Issuer
// map. Issuer is namespace-scoped, so the cluster-wide list passed by
// the handler can have name collisions across namespaces (an "ca"
// Issuer in namespace foo and another in namespace bar). Keying by
// namespaced name keeps them distinct.
func indexIssuersByNamespacedName(issuers []Issuer) map[string]Issuer {
	m := make(map[string]Issuer, len(issuers))
	for _, iss := range issuers {
		m[iss.Namespace+"/"+iss.Name] = iss
	}
	return m
}

// indexByName builds a name -> Issuer map. Used for ClusterIssuer,
// which is cluster-scoped and uniquely named.
func indexByName(issuers []Issuer) map[string]Issuer {
	m := make(map[string]Issuer, len(issuers))
	for _, iss := range issuers {
		m[iss.Name] = iss
	}
	return m
}
