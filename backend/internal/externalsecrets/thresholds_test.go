package externalsecrets

import (
	"log/slog"
	"testing"
)

// nullLogger discards everything; tests don't need to verify slog output.
var nullLogger = slog.New(slog.NewTextHandler(discardWriter{}, nil))

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func intPtr(n int) *int    { return &n }
func boolPtr(b bool) *bool { return &b }

func TestParseStaleAfterAnnotation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantOk  bool
		wantVal int
	}{
		{name: "empty", raw: "", wantOk: false},
		{name: "valid 5", raw: "5", wantOk: true, wantVal: 5},
		{name: "valid 60", raw: "60", wantOk: true, wantVal: 60},
		{name: "below floor 3", raw: "3", wantOk: false},
		{name: "below floor 1", raw: "1", wantOk: false},
		{name: "zero rejected", raw: "0", wantOk: false},
		{name: "negative rejected", raw: "-5", wantOk: false},
		{name: "non-numeric rejected", raw: "potato", wantOk: false},
		{name: "decimal rejected", raw: "5.5", wantOk: false},
		{name: "whitespace rejected", raw: " 5 ", wantOk: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val, ok := ParseStaleAfterAnnotation(tc.raw, "test", "x", nullLogger)
			if ok != tc.wantOk {
				t.Fatalf("ok=%v want=%v (val=%d)", ok, tc.wantOk, val)
			}
			if ok && val != tc.wantVal {
				t.Fatalf("val=%d want=%d", val, tc.wantVal)
			}
		})
	}
}

func TestParseBoolAnnotation(t *testing.T) {
	cases := []struct {
		raw     string
		wantOk  bool
		wantVal bool
	}{
		{raw: "", wantOk: false},
		{raw: "true", wantOk: true, wantVal: true},
		{raw: "false", wantOk: true, wantVal: false},
		{raw: "True", wantOk: true, wantVal: true},
		{raw: "1", wantOk: true, wantVal: true},
		{raw: "0", wantOk: true, wantVal: false},
		{raw: "yes", wantOk: false},
		{raw: "potato", wantOk: false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			val, ok := ParseBoolAnnotation(tc.raw)
			if ok != tc.wantOk {
				t.Fatalf("ok=%v want=%v", ok, tc.wantOk)
			}
			if ok && val != tc.wantVal {
				t.Fatalf("val=%v want=%v", val, tc.wantVal)
			}
		})
	}
}

func TestComputeStaleDefault(t *testing.T) {
	cases := []struct {
		name     string
		interval string
		want     int
	}{
		{name: "unset uses fallback", interval: "", want: 120},
		{name: "1h doubled", interval: "1h", want: 120},
		{name: "30m doubled", interval: "30m", want: 60},
		{name: "1m doubled clamps to floor", interval: "1m", want: MinStaleAfterMinutes},
		{name: "unparseable falls back", interval: "weekly", want: 120},
		{name: "zero falls back", interval: "0s", want: 120},
		{name: "10m doubled", interval: "10m", want: 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeStaleDefault(tc.interval)
			if got != tc.want {
				t.Fatalf("got=%d want=%d", got, tc.want)
			}
		})
	}
}

// newTypedES builds a minimal ExternalSecret for resolver tests. Override the
// returned struct's pointer fields to set per-cert annotation values.
func newTypedES(namespace, name, refKind, refName string) ExternalSecret {
	return ExternalSecret{
		Namespace: namespace,
		Name:      name,
		StoreRef: StoreRef{
			Name: refName,
			Kind: refKind,
		},
		RefreshInterval: "1h",
	}
}

func newTypedStore(namespace, name string, scope string) SecretStore {
	return SecretStore{
		Namespace: namespace,
		Name:      name,
		Scope:     scope,
	}
}

func TestResolveESOThresholds_HappyESOnly(t *testing.T) {
	es := newTypedES("apps", "db-creds", "SecretStore", "vault")
	es.StaleAfterMinutes = intPtr(45)

	stale, staleSource, _, _, _, _ := ResolveESOThresholds(es, nil, nil, nullLogger)
	if stale != 45 || staleSource != ThresholdSourceExternalSecret {
		t.Fatalf("stale=%d source=%s; want 45/externalsecret", stale, staleSource)
	}
}

func TestResolveESOThresholds_HappyStoreOnly(t *testing.T) {
	es := newTypedES("apps", "db-creds", "SecretStore", "vault")
	store := newTypedStore("apps", "vault", "Namespaced")
	store.StaleAfterMinutes = intPtr(60)

	stores := indexStoresByNamespacedName([]SecretStore{store})
	stale, staleSource, _, _, _, _ := ResolveESOThresholds(es, stores, nil, nullLogger)
	if stale != 60 || staleSource != ThresholdSourceSecretStore {
		t.Fatalf("stale=%d source=%s; want 60/secretstore", stale, staleSource)
	}
}

func TestResolveESOThresholds_ClusterStoreFallback(t *testing.T) {
	es := newTypedES("apps", "db-creds", "ClusterSecretStore", "platform-vault")
	cs := newTypedStore("", "platform-vault", "Cluster")
	cs.StaleAfterMinutes = intPtr(90)
	cs.AlertOnLifecycle = boolPtr(true)

	clusterStores := indexStoresByName([]SecretStore{cs})
	stale, staleSource, _, _, life, lifeSource := ResolveESOThresholds(es, nil, clusterStores, nullLogger)
	if stale != 90 || staleSource != ThresholdSourceClusterSecretStore {
		t.Fatalf("stale=%d source=%s; want 90/clustersecretstore", stale, staleSource)
	}
	if !life || lifeSource != ThresholdSourceClusterSecretStore {
		t.Fatalf("lifecycle=%v source=%s; want true/clustersecretstore", life, lifeSource)
	}
}

func TestResolveESOThresholds_DefaultEverywhere(t *testing.T) {
	es := newTypedES("apps", "db-creds", "SecretStore", "vault") // refresh "1h"
	stale, staleSource, rec, recSource, life, lifeSource := ResolveESOThresholds(es, nil, nil, nullLogger)
	if stale != 120 || staleSource != ThresholdSourceDefault {
		t.Fatalf("stale=%d source=%s; want 120/default (2×1h)", stale, staleSource)
	}
	if rec != DefaultAlertOnRecovery || recSource != ThresholdSourceDefault {
		t.Fatalf("rec=%v source=%s; want default false", rec, recSource)
	}
	if life != DefaultAlertOnLifecycle || lifeSource != ThresholdSourceDefault {
		t.Fatalf("life=%v source=%s; want default false", life, lifeSource)
	}
}

func TestResolveESOThresholds_DefaultUnsetRefresh(t *testing.T) {
	es := newTypedES("apps", "db-creds", "SecretStore", "vault")
	es.RefreshInterval = ""

	stale, staleSource, _, _, _, _ := ResolveESOThresholds(es, nil, nil, nullLogger)
	if stale != DefaultStaleAfterMinutesFallback || staleSource != ThresholdSourceDefault {
		t.Fatalf("stale=%d source=%s; want %d/default", stale, staleSource, DefaultStaleAfterMinutesFallback)
	}
}

func TestResolveESOThresholds_PerKeySourcesDiffer(t *testing.T) {
	// ES sets stale-after; Store sets alert-on-recovery. The two keys
	// resolve to different sources independently.
	es := newTypedES("apps", "db-creds", "SecretStore", "vault")
	es.StaleAfterMinutes = intPtr(15)

	store := newTypedStore("apps", "vault", "Namespaced")
	store.AlertOnRecovery = boolPtr(true)

	stores := indexStoresByNamespacedName([]SecretStore{store})
	stale, staleSource, rec, recSource, _, _ := ResolveESOThresholds(es, stores, nil, nullLogger)
	if stale != 15 || staleSource != ThresholdSourceExternalSecret {
		t.Fatalf("stale=%d source=%s; want 15/externalsecret", stale, staleSource)
	}
	if !rec || recSource != ThresholdSourceSecretStore {
		t.Fatalf("rec=%v source=%s; want true/secretstore", rec, recSource)
	}
}

func TestResolveESOThresholds_MissingReferencedStore(t *testing.T) {
	es := newTypedES("apps", "db-creds", "SecretStore", "nonexistent")
	// No store registered. Falls through to default.
	stale, staleSource, _, _, _, _ := ResolveESOThresholds(es, map[string]SecretStore{}, nil, nullLogger)
	if staleSource != ThresholdSourceDefault {
		t.Fatalf("source=%s; want default when referenced store is missing", staleSource)
	}
	if stale != 120 {
		t.Fatalf("stale=%d; want 120 (2×1h default)", stale)
	}
}

func TestResolveESOThresholds_RecoveryFalseAtESLevel(t *testing.T) {
	// ES explicitly sets AlertOnRecovery=false — this should win over a
	// store that sets true. False is a legal operator-set value, not nil.
	es := newTypedES("apps", "db-creds", "SecretStore", "vault")
	es.AlertOnRecovery = boolPtr(false)

	store := newTypedStore("apps", "vault", "Namespaced")
	store.AlertOnRecovery = boolPtr(true)

	stores := indexStoresByNamespacedName([]SecretStore{store})
	_, _, rec, recSource, _, _ := ResolveESOThresholds(es, stores, nil, nullLogger)
	if rec || recSource != ThresholdSourceExternalSecret {
		t.Fatalf("rec=%v source=%s; want false/externalsecret (ES override beats store)", rec, recSource)
	}
}

func TestApplyThresholds_PopulatesResolvedAndStatus(t *testing.T) {
	es := newTypedES("apps", "db-creds", "SecretStore", "vault")
	es.Status = StatusSynced // make stale overlay relevant
	store := newTypedStore("apps", "vault", "Namespaced")
	store.StaleAfterMinutes = intPtr(60)

	ess := []ExternalSecret{es}
	ApplyThresholds(ess, []SecretStore{store}, nil, nullLogger)

	if ess[0].StaleAfterMinutes == nil || *ess[0].StaleAfterMinutes != 60 {
		t.Fatalf("StaleAfterMinutes=%v; want 60", ess[0].StaleAfterMinutes)
	}
	if ess[0].StaleAfterMinutesSource != ThresholdSourceSecretStore {
		t.Fatalf("StaleAfterMinutesSource=%s; want secretstore", ess[0].StaleAfterMinutesSource)
	}
	if ess[0].AlertOnRecovery == nil || *ess[0].AlertOnRecovery != DefaultAlertOnRecovery {
		t.Fatalf("AlertOnRecovery=%v; want default", ess[0].AlertOnRecovery)
	}
	if ess[0].AlertOnRecoverySource != ThresholdSourceDefault {
		t.Fatalf("AlertOnRecoverySource=%s; want default", ess[0].AlertOnRecoverySource)
	}
}

func TestApplyThresholds_EmptySliceNoOp(t *testing.T) {
	// Should not panic; should not allocate.
	ApplyThresholds(nil, nil, nil, nullLogger)
	ApplyThresholds([]ExternalSecret{}, nil, nil, nullLogger)
}

func TestSanitizeSource(t *testing.T) {
	if sanitizeSource(ThresholdSourceExternalSecret) != ThresholdSourceExternalSecret {
		t.Fatal("legal value should pass through")
	}
	if sanitizeSource("bogus") != ThresholdSourceDefault {
		t.Fatal("illegal value should clamp to default")
	}
}
