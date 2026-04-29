package certmanager

import (
	"log/slog"
)

// ResolveCertThresholds walks the cert > issuer > clusterissuer >
// default chain for each threshold key independently and returns the
// effective (warn, crit, source) for the cert. Each key resolves on
// its own — a cert that sets warn but not crit picks up warn from the
// cert annotation and crit from the next layer down.
//
// source identifies the strongest layer that contributed a value. The
// precedence is certificate > issuer > clusterissuer > default; if the
// two keys resolve from different layers, source reflects the stronger
// one. The frontend uses this for the "Warns at: 60d (from Issuer X)"
// tooltip; for full per-key attribution, callers can inspect the
// underlying Issuer / ClusterIssuer pointers directly.
//
// If the resolved values violate crit < warn, both fall back to the
// package defaults and source becomes ThresholdSourceDefault. logger
// records the violation so operators can fix it; the alternative
// (silently using nonsensical values) is worse.
//
// issuersByName / clusterIssuersByName are simple "name -> Issuer"
// maps; pass empty maps when no issuers are listed (the chain still
// works, falling through to defaults).
func ResolveCertThresholds(
	cert Certificate,
	issuersByNSName map[string]Issuer,
	clusterIssuersByName map[string]Issuer,
	logger *slog.Logger,
) (warn, crit int, source ThresholdSource) {
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

	// Sanity-check: crit must be strictly less than warn. A
	// crit-equals-warn or crit-greater-than-warn pair is a misconfigured
	// override; fall back to defaults rather than render a confusing
	// state.
	if crit >= warn {
		if logger != nil {
			logger.Warn("certmanager: resolved thresholds violate crit < warn; falling back to defaults",
				"namespace", cert.Namespace,
				"name", cert.Name,
				"resolvedWarn", warn,
				"resolvedCrit", crit,
			)
		}
		return WarningThresholdDays, CriticalThresholdDays, ThresholdSourceDefault
	}

	return warn, crit, strongerSource(warnSource, critSource)
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
// is mutated in place AND returned so callers can chain.
//
// Issuer lookups are built once up front (O(certs * 1) instead of
// O(certs * issuers)). Pass nil for the issuer slices when none exist
// — the resolver falls through to defaults uniformly.
func ApplyThresholds(certs []Certificate, issuers, clusterIssuers []Issuer, logger *slog.Logger) []Certificate {
	if len(certs) == 0 {
		return certs
	}

	issuersByNSName := indexIssuersByNamespacedName(issuers)
	clusterIssuersByName := indexByName(clusterIssuers)

	for i := range certs {
		warn, crit, source := ResolveCertThresholds(certs[i], issuersByNSName, clusterIssuersByName, logger)
		certs[i].WarningThresholdDays = warn
		certs[i].CriticalThresholdDays = crit
		certs[i].ThresholdSource = source
		certs[i].Status = DeriveStatus(certs[i])
	}

	return certs
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
