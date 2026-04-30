package externalsecrets

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestComputeBaseStatus(t *testing.T) {
	cases := []struct {
		name          string
		readyStatus   string
		hasConditions bool
		want          Status
	}{
		{"ready-true", "True", true, StatusSynced},
		{"ready-false", "False", true, StatusSyncFailed},
		{"ready-unknown-explicit", "Unknown", true, StatusRefreshing},
		{"no-ready-but-has-other-conditions", "", true, StatusRefreshing},
		{"no-conditions-at-all", "", false, StatusUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeBaseStatus(tc.readyStatus, tc.hasConditions)
			if got != tc.want {
				t.Errorf("computeBaseStatus(%q, %v) = %q; want %q",
					tc.readyStatus, tc.hasConditions, got, tc.want)
			}
		})
	}
}

func TestDeriveStatus(t *testing.T) {
	now := time.Now()
	freshly := func(d time.Duration) *time.Time {
		t := now.Add(-d)
		return &t
	}

	cases := []struct {
		name string
		es   ExternalSecret
		want Status
	}{
		{
			name: "fresh-synced",
			es: ExternalSecret{
				Status:            StatusSynced,
				StaleAfterMinutes: 60,
				LastSyncTime:      freshly(5 * time.Minute),
				DriftStatus:       DriftInSync,
			},
			want: StatusSynced,
		},
		{
			name: "stale-overlay",
			es: ExternalSecret{
				Status:            StatusSynced,
				StaleAfterMinutes: 30,
				LastSyncTime:      freshly(45 * time.Minute),
			},
			want: StatusStale,
		},
		{
			name: "drift-overlay",
			es: ExternalSecret{
				Status:      StatusSynced,
				DriftStatus: DriftDrifted,
			},
			want: StatusDrifted,
		},
		{
			name: "drift-never-overrides-failure",
			es: ExternalSecret{
				Status:      StatusSyncFailed,
				DriftStatus: DriftDrifted,
			},
			want: StatusSyncFailed,
		},
		{
			name: "stale-never-overrides-failure",
			es: ExternalSecret{
				Status:            StatusSyncFailed,
				StaleAfterMinutes: 5,
				LastSyncTime:      freshly(60 * time.Minute),
			},
			want: StatusSyncFailed,
		},
		{
			name: "stale-wins-over-drift",
			es: ExternalSecret{
				Status:            StatusSynced,
				StaleAfterMinutes: 30,
				LastSyncTime:      freshly(60 * time.Minute),
				DriftStatus:       DriftDrifted,
			},
			want: StatusStale,
		},
		{
			name: "no-threshold-no-stale-overlay",
			es: ExternalSecret{
				Status:            StatusSynced,
				StaleAfterMinutes: 0, // resolver hasn't run
				LastSyncTime:      freshly(99 * time.Hour),
			},
			want: StatusSynced,
		},
		{
			name: "no-lastsync-no-stale-overlay",
			es: ExternalSecret{
				Status:            StatusSynced,
				StaleAfterMinutes: 30,
				LastSyncTime:      nil,
			},
			want: StatusSynced,
		},
		{
			name: "drift-unknown-no-overlay",
			es: ExternalSecret{
				Status:      StatusSynced,
				DriftStatus: DriftUnknown,
			},
			want: StatusSynced,
		},
		{
			name: "refreshing-stays-refreshing",
			es: ExternalSecret{
				Status:      StatusRefreshing,
				DriftStatus: DriftDrifted,
			},
			want: StatusRefreshing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveStatus(tc.es)
			if got != tc.want {
				t.Errorf("DeriveStatus(%+v) = %q; want %q", tc.es, got, tc.want)
			}
		})
	}
}

func TestThresholdSourceValid(t *testing.T) {
	cases := map[ThresholdSource]bool{
		ThresholdSourceDefault:            true,
		ThresholdSourceExternalSecret:     true,
		ThresholdSourceSecretStore:        true,
		ThresholdSourceClusterSecretStore: true,
		"":                                false,
		"junk":                            false,
		"issuer":                          false, // would have been valid in cert-manager — guard prevents accidental cross-pollination
	}
	for src, want := range cases {
		if got := src.Valid(); got != want {
			t.Errorf("ThresholdSource(%q).Valid() = %v; want %v", src, got, want)
		}
	}
}

func TestNormalizeExternalSecret_Happy(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":      "db-creds",
				"namespace": "apps",
				"uid":       "uid-001",
			},
			"spec": map[string]any{
				"refreshInterval": "1h",
				"secretStoreRef": map[string]any{
					"name": "vault-store",
					"kind": "SecretStore",
				},
				"target": map[string]any{
					"name": "synced-db-creds",
				},
			},
			"status": map[string]any{
				"refreshTime":           now.Format(time.RFC3339),
				"syncedResourceVersion": "12345",
				"conditions": []any{
					map[string]any{
						"type":   "Ready",
						"status": "True",
						"reason": "SecretSynced",
					},
				},
			},
		},
	}

	es := normalizeExternalSecret(u)
	if es.Name != "db-creds" || es.Namespace != "apps" || es.UID != "uid-001" {
		t.Fatalf("identity wrong: %+v", es)
	}
	if es.Status != StatusSynced {
		t.Errorf("Status = %q; want %q", es.Status, StatusSynced)
	}
	if es.DriftStatus != DriftUnknown {
		t.Errorf("DriftStatus = %q; want %q (list view never resolves drift)", es.DriftStatus, DriftUnknown)
	}
	if es.StoreRef.Name != "vault-store" || es.StoreRef.Kind != "SecretStore" {
		t.Errorf("StoreRef = %+v; want vault-store/SecretStore", es.StoreRef)
	}
	if es.TargetSecretName != "synced-db-creds" {
		t.Errorf("TargetSecretName = %q; want synced-db-creds", es.TargetSecretName)
	}
	if es.RefreshInterval != "1h" {
		t.Errorf("RefreshInterval = %q; want 1h", es.RefreshInterval)
	}
	if es.SyncedResourceVersion != "12345" {
		t.Errorf("SyncedResourceVersion = %q; want 12345", es.SyncedResourceVersion)
	}
	if es.LastSyncTime == nil || !es.LastSyncTime.Equal(now) {
		t.Errorf("LastSyncTime = %v; want %v", es.LastSyncTime, now)
	}
	if es.ReadyReason != "SecretSynced" {
		t.Errorf("ReadyReason = %q; want SecretSynced", es.ReadyReason)
	}
}

func TestNormalizeExternalSecret_FailureCondition(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{"name": "x", "namespace": "y", "uid": "z"},
			"spec":     map[string]any{},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":    "Ready",
						"status":  "False",
						"reason":  "AuthFailed",
						"message": "vault auth refused",
					},
				},
			},
		},
	}
	es := normalizeExternalSecret(u)
	if es.Status != StatusSyncFailed {
		t.Errorf("Status = %q; want %q", es.Status, StatusSyncFailed)
	}
	if es.ReadyReason != "AuthFailed" {
		t.Errorf("ReadyReason = %q", es.ReadyReason)
	}
	if es.ReadyMessage != "vault auth refused" {
		t.Errorf("ReadyMessage = %q", es.ReadyMessage)
	}
}

func TestNormalizeExternalSecret_NoConditions(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{"name": "x", "namespace": "y", "uid": "z"},
			"spec":     map[string]any{},
			"status":   map[string]any{},
		},
	}
	es := normalizeExternalSecret(u)
	if es.Status != StatusUnknown {
		t.Errorf("Status = %q; want %q (no conditions = brand new ES)", es.Status, StatusUnknown)
	}
}

func TestNormalizeExternalSecret_TargetTemplateName(t *testing.T) {
	// target.template.metadata.name fallback when target.name is absent
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{"name": "x", "namespace": "y", "uid": "z"},
			"spec": map[string]any{
				"target": map[string]any{
					"template": map[string]any{
						"metadata": map[string]any{
							"name": "tmpl-secret-name",
						},
					},
				},
			},
			"status": map[string]any{},
		},
	}
	es := normalizeExternalSecret(u)
	if es.TargetSecretName != "tmpl-secret-name" {
		t.Errorf("TargetSecretName = %q; want tmpl-secret-name", es.TargetSecretName)
	}
}

func TestNormalizeSecretStore_ProviderDetection(t *testing.T) {
	cases := []struct {
		name         string
		spec         map[string]any
		wantProvider string
		wantReady    bool
	}{
		{
			name: "vault-ready",
			spec: map[string]any{
				"provider": map[string]any{
					"vault": map[string]any{
						"server": "https://vault.example.com",
						"path":   "secret",
					},
				},
			},
			wantProvider: "vault",
		},
		{
			name: "aws-secretsmanager",
			spec: map[string]any{
				"provider": map[string]any{
					"aws": map[string]any{
						"service": "SecretsManager",
						"region":  "us-east-1",
					},
				},
			},
			wantProvider: "aws",
		},
		{
			name: "kubernetes-provider",
			spec: map[string]any{
				"provider": map[string]any{
					"kubernetes": map[string]any{
						"remoteNamespace": "source-ns",
					},
				},
			},
			wantProvider: "kubernetes",
		},
		{
			name:         "no-provider",
			spec:         map[string]any{},
			wantProvider: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{"name": "s", "namespace": "n", "uid": "u"},
					"spec":     tc.spec,
					"status": map[string]any{
						"conditions": []any{
							map[string]any{"type": "Ready", "status": "True"},
						},
					},
				},
			}
			s := normalizeSecretStore(u, "Namespaced")
			if s.Provider != tc.wantProvider {
				t.Errorf("Provider = %q; want %q", s.Provider, tc.wantProvider)
			}
			if s.Scope != "Namespaced" {
				t.Errorf("Scope = %q; want Namespaced", s.Scope)
			}
			if s.Status != StatusSynced {
				t.Errorf("Status = %q; want %q", s.Status, StatusSynced)
			}
			if !s.Ready {
				t.Errorf("Ready = false; want true")
			}
		})
	}
}

func TestNormalizeSecretStore_ClusterScope(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{"name": "global", "uid": "uid-css"},
			"spec":     map[string]any{"provider": map[string]any{"vault": map[string]any{}}},
			"status":   map[string]any{},
		},
	}
	s := normalizeSecretStore(u, "Cluster")
	if s.Namespace != "" {
		t.Errorf("Namespace = %q; want empty for ClusterSecretStore", s.Namespace)
	}
	if s.Scope != "Cluster" {
		t.Errorf("Scope = %q", s.Scope)
	}
}

func TestNormalizeClusterExternalSecret(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{"name": "fanout", "uid": "uid-ces"},
			"spec": map[string]any{
				"externalSecretSpec": map[string]any{
					"refreshInterval": "30m",
					"secretStoreRef": map[string]any{
						"name": "global-vault",
						"kind": "ClusterSecretStore",
					},
					"target": map[string]any{"name": "shared-creds"},
				},
				"namespaces": []any{"apps", "platform"},
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{
						"tier": "production",
					},
				},
			},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
				},
				"provisionedNamespaces": []any{"apps", "platform"},
				"failedNamespaces": []any{
					map[string]any{"namespace": "edge", "reason": "PermissionDenied"},
				},
			},
		},
	}

	ces := normalizeClusterExternalSecret(u)
	if ces.Name != "fanout" || ces.UID != "uid-ces" {
		t.Errorf("identity wrong: %+v", ces)
	}
	if ces.Status != StatusSynced {
		t.Errorf("Status = %q", ces.Status)
	}
	if ces.StoreRef.Name != "global-vault" || ces.StoreRef.Kind != "ClusterSecretStore" {
		t.Errorf("StoreRef = %+v", ces.StoreRef)
	}
	if ces.TargetSecretName != "shared-creds" {
		t.Errorf("TargetSecretName = %q", ces.TargetSecretName)
	}
	if len(ces.Namespaces) != 2 {
		t.Errorf("Namespaces = %v", ces.Namespaces)
	}
	if len(ces.ProvisionedNamespaces) != 2 {
		t.Errorf("ProvisionedNamespaces = %v", ces.ProvisionedNamespaces)
	}
	if len(ces.FailedNamespaces) != 1 || ces.FailedNamespaces[0] != "edge" {
		t.Errorf("FailedNamespaces = %v", ces.FailedNamespaces)
	}
	if len(ces.NamespaceSelectors) == 0 {
		t.Errorf("NamespaceSelectors = %v; expected at least 1 from matchLabels", ces.NamespaceSelectors)
	}
}

func TestNormalizePushSecret(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{"name": "push-x", "namespace": "apps", "uid": "uid-push"},
			"spec": map[string]any{
				"refreshInterval": "1h",
				"selector": map[string]any{
					"secret": map[string]any{"name": "source-secret"},
				},
				"secretStoreRefs": []any{
					map[string]any{"name": "vault-1", "kind": "SecretStore"},
					map[string]any{"name": "vault-cluster", "kind": "ClusterSecretStore"},
				},
			},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
				},
			},
		},
	}

	ps := normalizePushSecret(u)
	if ps.Name != "push-x" || ps.SourceSecretName != "source-secret" {
		t.Errorf("PushSecret identity wrong: %+v", ps)
	}
	if len(ps.StoreRefs) != 2 {
		t.Errorf("StoreRefs len = %d; want 2", len(ps.StoreRefs))
	}
	if ps.StoreRefs[0].Kind != "SecretStore" || ps.StoreRefs[1].Kind != "ClusterSecretStore" {
		t.Errorf("StoreRefs kinds: %+v", ps.StoreRefs)
	}
	if ps.Status != StatusSynced {
		t.Errorf("Status = %q", ps.Status)
	}
}

func TestParseTimeField(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	good := map[string]any{"refreshTime": now.Format(time.RFC3339)}
	if got := parseTimeField(good, "refreshTime"); got == nil || !got.Equal(now) {
		t.Errorf("parseTimeField(good) = %v; want %v", got, now)
	}

	bad := map[string]any{"refreshTime": "not-a-time"}
	if got := parseTimeField(bad, "refreshTime"); got != nil {
		t.Errorf("parseTimeField(bad) = %v; want nil", got)
	}

	missing := map[string]any{}
	if got := parseTimeField(missing, "refreshTime"); got != nil {
		t.Errorf("parseTimeField(missing) = %v; want nil", got)
	}
}
