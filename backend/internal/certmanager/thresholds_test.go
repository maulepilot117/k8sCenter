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
	warn, crit, source := ResolveCertThresholds(cert, nil, nil, slog.Default())

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

	warn, crit, source := ResolveCertThresholds(cert, issuers, nil, slog.Default())

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

	warn, crit, source := ResolveCertThresholds(cert, issuers, nil, slog.Default())

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

	warn, crit, source := ResolveCertThresholds(cert, nil, clusterIssuers, slog.Default())

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

	warn, crit, source := ResolveCertThresholds(cert, issuers, nil, slog.Default())

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
	warn, crit, source := ResolveCertThresholds(cert, nil, nil, slog.Default())

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

	warn, _, source := ResolveCertThresholds(cert, nil, clusterIssuers, slog.Default())

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

	warn, crit, source := ResolveCertThresholds(cert, nil, nil, slog.Default())

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

	out := ApplyThresholds([]Certificate{cert}, issuers, nil, slog.Default())

	if len(out) != 1 {
		t.Fatalf("ApplyThresholds returned %d certs, want 1", len(out))
	}
	if out[0].Status != StatusExpiring {
		t.Errorf("Status = %q, want %q (50d <= resolved warn 60d should fire Expiring)", out[0].Status, StatusExpiring)
	}
	if out[0].WarningThresholdDays != 60 {
		t.Errorf("WarningThresholdDays = %d, want 60 (issuer override)", out[0].WarningThresholdDays)
	}
	if out[0].ThresholdSource != ThresholdSourceIssuer {
		t.Errorf("ThresholdSource = %q, want %q", out[0].ThresholdSource, ThresholdSourceIssuer)
	}
}

func TestApplyThresholds_EmptySliceIsNoOp(t *testing.T) {
	out := ApplyThresholds(nil, nil, nil, slog.Default())
	if out != nil {
		t.Errorf("ApplyThresholds(nil) = %v, want nil (no-op)", out)
	}
}

func TestApplyThresholds_AcmeShortWarnNotInExpiringYet(t *testing.T) {
	// ACME certs auto-renew at ~30 days. With a short warn=14 override,
	// a cert at 20 days remaining should NOT show Expiring — auto-renew
	// will handle it before the warning fires.
	cert := makeCert("apps", "ingress", "ClusterIssuer", "letsencrypt-prod", 14, 3, intp(20))
	clusterIssuers := []Issuer{{Name: "letsencrypt-prod"}}

	out := ApplyThresholds([]Certificate{cert}, nil, clusterIssuers, slog.Default())

	if out[0].Status != StatusReady {
		t.Errorf("Status = %q, want %q (20d > resolved warn 14d should stay Ready)", out[0].Status, StatusReady)
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

	out := ApplyThresholds([]Certificate{cert}, nil, nil, slog.Default())

	if out[0].Status != StatusExpired {
		t.Errorf("Status = %q, want %q", out[0].Status, StatusExpired)
	}
}

