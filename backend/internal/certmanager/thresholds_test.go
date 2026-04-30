package certmanager

import (
	"log/slog"
	"testing"
)

// intp returns a pointer to the given int. Used inline for issuer
// pointer-field test fixtures.
func intp(n int) *int { return &n }

// makeCert is a small fixture builder for threshold tests; only the
// fields the resolver consults are populated.
func makeCert(ns, name string, refKind, refName string, certWarn, certCrit int, days *int) Certificate {
	return Certificate{
		Name:                  name,
		Namespace:             ns,
		Status:                StatusReady,
		IssuerRef:             IssuerRef{Name: refName, Kind: refKind, Group: "cert-manager.io"},
		DaysRemaining:         days,
		WarningThresholdDays:  certWarn,
		CriticalThresholdDays: certCrit,
	}
}

func TestResolveCertThresholds_DefaultWhenNoOverrides(t *testing.T) {
	cert := makeCert("foo", "c1", "Issuer", "ca", 0, 0, intp(20))
	warn, crit, source, _ := ResolveCertThresholds(cert, nil, nil, slog.Default())

	if warn != WarningThresholdDays || crit != CriticalThresholdDays {
		t.Errorf("got (%d, %d), want package defaults (%d, %d)", warn, crit, WarningThresholdDays, CriticalThresholdDays)
	}
	if source != ThresholdSourceDefault {
		t.Errorf("source = %q, want %q", source, ThresholdSourceDefault)
	}
}

func TestResolveCertThresholds_CertAnnotationWins(t *testing.T) {
	cert := makeCert("foo", "c1", "Issuer", "ca", 60, 14, intp(20))
	issuers := map[string]Issuer{"foo/ca": {Name: "ca", Namespace: "foo", WarningThresholdDays: intp(45), CriticalThresholdDays: intp(7)}}

	warn, crit, source, _ := ResolveCertThresholds(cert, issuers, nil, slog.Default())

	if warn != 60 || crit != 14 {
		t.Errorf("got (%d, %d), want cert-level (60, 14)", warn, crit)
	}
	if source != ThresholdSourceCertificate {
		t.Errorf("source = %q, want %q", source, ThresholdSourceCertificate)
	}
}

func TestResolveCertThresholds_IssuerInheritance(t *testing.T) {
	cert := makeCert("foo", "c1", "Issuer", "ca", 0, 0, intp(20))
	issuers := map[string]Issuer{"foo/ca": {Name: "ca", Namespace: "foo", WarningThresholdDays: intp(45), CriticalThresholdDays: intp(10)}}

	warn, crit, source, _ := ResolveCertThresholds(cert, issuers, nil, slog.Default())

	if warn != 45 || crit != 10 {
		t.Errorf("got (%d, %d), want issuer-level (45, 10)", warn, crit)
	}
	if source != ThresholdSourceIssuer {
		t.Errorf("source = %q, want %q", source, ThresholdSourceIssuer)
	}
}

func TestResolveCertThresholds_ClusterIssuerInheritance(t *testing.T) {
	cert := makeCert("foo", "c1", "ClusterIssuer", "letsencrypt-prod", 0, 0, intp(20))
	clusterIssuers := map[string]Issuer{"letsencrypt-prod": {Name: "letsencrypt-prod", WarningThresholdDays: intp(14), CriticalThresholdDays: intp(3)}}

	warn, crit, source, _ := ResolveCertThresholds(cert, nil, clusterIssuers, slog.Default())

	if warn != 14 || crit != 3 {
		t.Errorf("got (%d, %d), want clusterissuer-level (14, 3)", warn, crit)
	}
	if source != ThresholdSourceClusterIssuer {
		t.Errorf("source = %q, want %q", source, ThresholdSourceClusterIssuer)
	}
}

func TestResolveCertThresholds_MixedSourcesPickStrongest(t *testing.T) {
	// Cert sets warn only; issuer sets crit only. Aggregate source
	// should be "certificate" since that's the strongest layer that
	// contributed.
	cert := makeCert("foo", "c1", "Issuer", "ca", 60, 0, intp(20))
	issuers := map[string]Issuer{"foo/ca": {Name: "ca", Namespace: "foo", CriticalThresholdDays: intp(14)}}

	warn, crit, source, _ := ResolveCertThresholds(cert, issuers, nil, slog.Default())

	if warn != 60 || crit != 14 {
		t.Errorf("got (%d, %d), want (60, 14)", warn, crit)
	}
	if source != ThresholdSourceCertificate {
		t.Errorf("source = %q, want %q (certificate is the stronger contributor)", source, ThresholdSourceCertificate)
	}
}

func TestResolveCertThresholds_MissingIssuerFallsThrough(t *testing.T) {
	cert := makeCert("foo", "c1", "Issuer", "missing-ca", 0, 0, intp(20))
	// Empty issuer map — the named issuer doesn't exist.
	warn, crit, source, _ := ResolveCertThresholds(cert, nil, nil, slog.Default())

	if warn != WarningThresholdDays || crit != CriticalThresholdDays {
		t.Errorf("got (%d, %d), want defaults", warn, crit)
	}
	if source != ThresholdSourceDefault {
		t.Errorf("source = %q, want %q", source, ThresholdSourceDefault)
	}
}

func TestResolveCertThresholds_KindMismatchDoesNotCrossPollinate(t *testing.T) {
	// Cert references an Issuer named "ca". A ClusterIssuer also named
	// "ca" exists with overrides. The resolver must NOT use the
	// ClusterIssuer's overrides — different kinds, different objects.
	cert := makeCert("foo", "c1", "Issuer", "ca", 0, 0, intp(20))
	clusterIssuers := map[string]Issuer{"ca": {Name: "ca", WarningThresholdDays: intp(60)}}

	warn, _, source, _ := ResolveCertThresholds(cert, nil, clusterIssuers, slog.Default())

	if warn != WarningThresholdDays {
		t.Errorf("warn = %d, want default %d (must not cross-pollinate from ClusterIssuer of same name)", warn, WarningThresholdDays)
	}
	if source != ThresholdSourceDefault {
		t.Errorf("source = %q, want %q", source, ThresholdSourceDefault)
	}
}

func TestResolveCertThresholds_CritGteWarnFallsBackToDefaults(t *testing.T) {
	// Operator misconfigured the cert: crit >= warn. Resolver falls back
	// to package defaults rather than render a nonsensical state.
	cert := makeCert("foo", "c1", "Issuer", "ca", 14, 30, intp(20))

	warn, crit, source, _ := ResolveCertThresholds(cert, nil, nil, slog.Default())

	if warn != WarningThresholdDays || crit != CriticalThresholdDays {
		t.Errorf("got (%d, %d), want defaults — crit > warn must fall back", warn, crit)
	}
	if source != ThresholdSourceDefault {
		t.Errorf("source = %q, want %q after fallback", source, ThresholdSourceDefault)
	}
}

func TestApplyThresholds_StatusOverlayUsesResolvedWarn(t *testing.T) {
	// Cert with NotAfter 50 days out, no annotations, references an
	// Issuer with warn=60 annotation. After ApplyThresholds, Status
	// should be Expiring (50 <= resolved warn=60) — not Ready (which
	// would be the result if the resolver used the package default 30).
	cert := makeCert("foo", "long-runway", "Issuer", "internal-ca", 0, 0, intp(50))
	issuers := []Issuer{{Name: "internal-ca", Namespace: "foo", WarningThresholdDays: intp(60)}}

	certs := []Certificate{cert}
	ApplyThresholds(certs, issuers, nil, slog.Default())

	if certs[0].Status != StatusExpiring {
		t.Errorf("Status = %q, want %q (50d <= resolved warn 60d should fire Expiring)", certs[0].Status, StatusExpiring)
	}
	if certs[0].WarningThresholdDays != 60 {
		t.Errorf("WarningThresholdDays = %d, want 60 (issuer override)", certs[0].WarningThresholdDays)
	}
	if certs[0].ThresholdSource != ThresholdSourceIssuer {
		t.Errorf("ThresholdSource = %q, want %q", certs[0].ThresholdSource, ThresholdSourceIssuer)
	}
	if certs[0].WarningThresholdSource != ThresholdSourceIssuer {
		t.Errorf("WarningThresholdSource = %q, want %q (per-key attribution)", certs[0].WarningThresholdSource, ThresholdSourceIssuer)
	}
	if certs[0].CriticalThresholdSource != ThresholdSourceDefault {
		t.Errorf("CriticalThresholdSource = %q, want %q (issuer didn't set crit)", certs[0].CriticalThresholdSource, ThresholdSourceDefault)
	}
	if certs[0].ThresholdConflict {
		t.Errorf("ThresholdConflict = true, want false (warn=60 > crit=7 default is valid)")
	}
}

func TestApplyThresholds_EmptySliceIsNoOp(t *testing.T) {
	// nil slice survives unchanged. Don't panic.
	ApplyThresholds(nil, nil, nil, slog.Default())
}

func TestApplyThresholds_AcmeShortWarnNotInExpiringYet(t *testing.T) {
	// ACME certs auto-renew at ~30 days. With a short warn=14 override,
	// a cert at 20 days remaining should NOT show Expiring — auto-renew
	// will handle it before the warning fires.
	cert := makeCert("apps", "ingress", "ClusterIssuer", "letsencrypt-prod", 14, 3, intp(20))
	clusterIssuers := []Issuer{{Name: "letsencrypt-prod"}}

	certs := []Certificate{cert}
	ApplyThresholds(certs, nil, clusterIssuers, slog.Default())

	if certs[0].Status != StatusReady {
		t.Errorf("Status = %q, want %q (20d > resolved warn 14d should stay Ready)", certs[0].Status, StatusReady)
	}
}

func TestResolveCertThresholds_InverseKindMismatchDoesNotCrossPollinate(t *testing.T) {
	// Mirror of KindMismatchDoesNotCrossPollinate — cert references
	// kind=ClusterIssuer named "ca", but only an Issuer of the same
	// name is in scope. Resolver must NOT pick up the Issuer's
	// override; symmetry with the Issuer-side test.
	cert := makeCert("foo", "c1", "ClusterIssuer", "ca", 0, 0, intp(20))
	issuers := map[string]Issuer{"foo/ca": {Name: "ca", Namespace: "foo", WarningThresholdDays: intp(60)}}

	warn, _, source, _ := ResolveCertThresholds(cert, issuers, nil, slog.Default())

	if warn != WarningThresholdDays {
		t.Errorf("warn = %d, want default %d (must not cross-pollinate from Issuer of same name)", warn, WarningThresholdDays)
	}
	if source != ThresholdSourceDefault {
		t.Errorf("source = %q, want %q", source, ThresholdSourceDefault)
	}
}

func TestApplyThresholds_CertAnnotationWinsThroughPublicAPI(t *testing.T) {
	// Cert sets warn=14, crit=3 directly (annotation parsed in
	// normalizeCertificate). Issuer sets warn=60. ApplyThresholds must
	// honor the cert-level values, surface "certificate" on both
	// per-key sources, and overlay Status=Expiring at 13d.
	cert := makeCert("foo", "tight", "Issuer", "internal-ca", 14, 3, intp(13))
	issuers := []Issuer{{Name: "internal-ca", Namespace: "foo", WarningThresholdDays: intp(60)}}

	certs := []Certificate{cert}
	ApplyThresholds(certs, issuers, nil, slog.Default())

	if certs[0].WarningThresholdDays != 14 {
		t.Errorf("WarningThresholdDays = %d, want 14 (cert annotation wins)", certs[0].WarningThresholdDays)
	}
	if certs[0].CriticalThresholdDays != 3 {
		t.Errorf("CriticalThresholdDays = %d, want 3 (cert annotation wins)", certs[0].CriticalThresholdDays)
	}
	if certs[0].WarningThresholdSource != ThresholdSourceCertificate {
		t.Errorf("WarningThresholdSource = %q, want %q", certs[0].WarningThresholdSource, ThresholdSourceCertificate)
	}
	if certs[0].CriticalThresholdSource != ThresholdSourceCertificate {
		t.Errorf("CriticalThresholdSource = %q, want %q", certs[0].CriticalThresholdSource, ThresholdSourceCertificate)
	}
	if certs[0].Status != StatusExpiring {
		t.Errorf("Status = %q, want %q (13d <= warn=14)", certs[0].Status, StatusExpiring)
	}
}

func TestApplyThresholds_ClusterIssuerInheritanceThroughPublicAPI(t *testing.T) {
	// Cert in apps namespace, no annotations. ClusterIssuer
	// letsencrypt-prod has tight warn=14, crit=3. Cert at 10 days
	// remaining should be Critical (10 <= 3? No, 10 > 3). Wait — 10 > 3
	// means it's in the warning band (10 <= warn=14). Status should be
	// Expiring. Source should be clusterissuer.
	cert := makeCert("apps", "ingress", "ClusterIssuer", "letsencrypt-prod", 0, 0, intp(10))
	clusterIssuers := []Issuer{{Name: "letsencrypt-prod", WarningThresholdDays: intp(14), CriticalThresholdDays: intp(3)}}

	certs := []Certificate{cert}
	ApplyThresholds(certs, nil, clusterIssuers, slog.Default())

	if certs[0].WarningThresholdSource != ThresholdSourceClusterIssuer {
		t.Errorf("WarningThresholdSource = %q, want %q", certs[0].WarningThresholdSource, ThresholdSourceClusterIssuer)
	}
	if certs[0].CriticalThresholdSource != ThresholdSourceClusterIssuer {
		t.Errorf("CriticalThresholdSource = %q, want %q", certs[0].CriticalThresholdSource, ThresholdSourceClusterIssuer)
	}
	if certs[0].Status != StatusExpiring {
		t.Errorf("Status = %q, want %q", certs[0].Status, StatusExpiring)
	}
}

func TestApplyThresholds_MixedSourcesPerKey(t *testing.T) {
	// Cert sets warn=60 only; issuer sets crit=14 only. Per-key sources
	// must reflect the actual layer that supplied each value.
	// Aggregate source picks the strongest (certificate).
	cert := makeCert("foo", "mixed", "Issuer", "ca", 60, 0, intp(50))
	issuers := []Issuer{{Name: "ca", Namespace: "foo", CriticalThresholdDays: intp(14)}}

	certs := []Certificate{cert}
	ApplyThresholds(certs, issuers, nil, slog.Default())

	if certs[0].WarningThresholdSource != ThresholdSourceCertificate {
		t.Errorf("WarningThresholdSource = %q, want %q", certs[0].WarningThresholdSource, ThresholdSourceCertificate)
	}
	if certs[0].CriticalThresholdSource != ThresholdSourceIssuer {
		t.Errorf("CriticalThresholdSource = %q, want %q", certs[0].CriticalThresholdSource, ThresholdSourceIssuer)
	}
	if certs[0].ThresholdSource != ThresholdSourceCertificate {
		t.Errorf("aggregate ThresholdSource = %q, want %q (strongest contributor)", certs[0].ThresholdSource, ThresholdSourceCertificate)
	}
}

func TestApplyThresholds_ConflictFlaggedAndDefaultsApplied(t *testing.T) {
	// Cert sets warn=14, issuer sets crit=20. Combined: crit (20) >=
	// warn (14) — invalid. Resolver must fall back to defaults AND
	// flag ThresholdConflict=true so the UI can explain rather than
	// silently show "Default" with no signal.
	cert := makeCert("foo", "conflicted", "Issuer", "ca", 14, 0, intp(20))
	issuers := []Issuer{{Name: "ca", Namespace: "foo", CriticalThresholdDays: intp(20)}}

	certs := []Certificate{cert}
	ApplyThresholds(certs, issuers, nil, slog.Default())

	if !certs[0].ThresholdConflict {
		t.Error("ThresholdConflict = false, want true (warn=14, crit=20 violates crit < warn)")
	}
	if certs[0].WarningThresholdDays != WarningThresholdDays {
		t.Errorf("WarningThresholdDays = %d, want default %d after conflict fallback", certs[0].WarningThresholdDays, WarningThresholdDays)
	}
	if certs[0].ThresholdSource != ThresholdSourceDefault {
		t.Errorf("ThresholdSource = %q, want %q after conflict fallback", certs[0].ThresholdSource, ThresholdSourceDefault)
	}
}

// Ensure StatusExpired survives ApplyThresholds untouched even when
// thresholds are unusual; the expired check runs in computeStatus and
// is independent of the configurable threshold.
func TestApplyThresholds_ExpiredStaysExpiredRegardlessOfThresholds(t *testing.T) {
	days := -1
	cert := Certificate{
		Name:          "expired",
		Status:        StatusExpired,
		DaysRemaining: &days,
		IssuerRef:     IssuerRef{Name: "ca", Kind: "Issuer"},
		// Misconfigured thresholds shouldn't change the verdict.
		WarningThresholdDays: 9999,
	}

	certs := []Certificate{cert}
	ApplyThresholds(certs, nil, nil, slog.Default())

	if certs[0].Status != StatusExpired {
		t.Errorf("Status = %q, want %q", certs[0].Status, StatusExpired)
	}
}

