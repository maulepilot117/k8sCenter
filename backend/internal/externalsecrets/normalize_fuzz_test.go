package externalsecrets

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// unstructuredFromFuzz decodes fuzz bytes into an *unstructured.Unstructured.
// Inputs that don't decode to a JSON/YAML object are skipped — the seed corpus
// carries the structural diversity and the mutator explores around it.
func unstructuredFromFuzz(data []byte) (*unstructured.Unstructured, bool) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return &unstructured.Unstructured{Object: m}, true
}

// FuzzExternalSecretsNormalizers asserts every ESO normalizer is crash-safe on
// arbitrary/adversarial unstructured input. Oracle: no panic; zero-values fine.
func FuzzExternalSecretsNormalizers(f *testing.F) {
	// ── realistic valid objects ───────────────────────────────────────────────

	// ExternalSecret with spec.data + spec.secretStoreRef + status.conditions
	f.Add([]byte(`
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: my-es
  namespace: default
  annotations:
    kubecenter.io/eso-stale-after-minutes: "30"
    kubecenter.io/eso-alert-on-recovery: "true"
    kubecenter.io/eso-alert-on-lifecycle: "false"
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: vault-backend
    kind: SecretStore
  target:
    name: my-secret
  data:
    - secretKey: api-key
      remoteRef:
        key: path/to/secret
        property: api_key
status:
  refreshTime: "2026-06-20T10:00:00Z"
  syncedResourceVersion: abc123
  conditions:
    - type: Ready
      status: "True"
      reason: SecretSynced
      message: Secret was synced
`))

	// ClusterExternalSecret
	f.Add([]byte(`
apiVersion: external-secrets.io/v1beta1
kind: ClusterExternalSecret
metadata:
  name: my-ces
spec:
  externalSecretName: my-es
  namespaces:
    - default
    - production
  namespaceSelector:
    matchLabels:
      env: prod
  externalSecretSpec:
    refreshInterval: 30m
    secretStoreRef:
      name: vault-backend
      kind: ClusterSecretStore
    target:
      name: shared-secret
status:
  conditions:
    - type: Ready
      status: "False"
      reason: NamespaceError
      message: failed to provision namespace production
  provisionedNamespaces:
    - default
  failedNamespaces:
    - namespace: production
      reason: access denied
`))

	// SecretStore (Namespaced) with provider spec
	f.Add([]byte(`
apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: vault-backend
  namespace: default
spec:
  provider:
    vault:
      server: https://vault.example.com
      path: secret
      version: v2
      auth:
        kubernetes:
          mountPath: kubernetes
          role: my-role
status:
  conditions:
    - type: Ready
      status: "True"
      reason: Valid
      message: Store validation succeeded
`))

	// ClusterSecretStore (Cluster scope)
	f.Add([]byte(`
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: aws-backend
  annotations:
    kubecenter.io/eso-stale-after-minutes: "60"
spec:
  provider:
    aws:
      service: SecretsManager
      region: us-east-1
      auth:
        secretRef:
          accessKeyIDSecretRef:
            name: awssm-secret
            key: access-key
status:
  conditions:
    - type: Ready
      status: Unknown
      reason: Validating
      message: validating store configuration
`))

	// PushSecret
	f.Add([]byte(`
apiVersion: external-secrets.io/v1beta1
kind: PushSecret
metadata:
  name: my-push
  namespace: default
spec:
  refreshInterval: 2h
  selector:
    secret:
      name: source-secret
  secretStoreRefs:
    - name: vault-backend
      kind: SecretStore
    - name: aws-backend
      kind: ClusterSecretStore
  data:
    - match:
        secretKey: password
        remoteRef:
          remoteKey: path/to/remote
status:
  refreshTime: "2026-06-21T08:00:00Z"
  conditions:
    - type: Ready
      status: "True"
      reason: Synced
      message: PushSecret synced successfully
`))

	// ── malformed / adversarial teeth ────────────────────────────────────────

	f.Add([]byte(`{}`))
	f.Add([]byte(`{"metadata":"oops"}`))
	f.Add([]byte(`{"spec":[],"status":"x"}`))
	f.Add([]byte(`{"spec":{"data":"notalist"}}`))
	f.Add([]byte(`{"status":{"conditions":{}}}`))

	// deeply nested wrong types
	f.Add([]byte(`{"spec":{"secretStoreRef":42}}`))
	f.Add([]byte(`{"spec":{"target":true}}`))
	f.Add([]byte(`{"spec":{"externalSecretSpec":false}}`))
	f.Add([]byte(`{"spec":{"namespaceSelectors":"notalist"}}`))
	f.Add([]byte(`{"spec":{"secretStoreRefs":{"key":"val"}}}`))
	f.Add([]byte(`{"spec":{"provider":123}}`))
	f.Add([]byte(`{"spec":{"selector":{"secret":["a","b"]}}}`))

	// status.conditions with wrong element types
	f.Add([]byte(`{"status":{"conditions":["notanobject",42,null]}}`))
	f.Add([]byte(`{"status":{"conditions":[{"type":"Ready","status":99}]}}`))
	f.Add([]byte(`{"status":{"failedNamespaces":["notanobject"]}}`))

	// refreshTime / timestamps in unexpected formats
	f.Add([]byte(`{"status":{"refreshTime":"not-a-time"}}`))
	f.Add([]byte(`{"status":{"refreshTime":12345}}`))
	f.Add([]byte(`{"status":{"refreshTime":null}}`))

	// annotations with invalid values
	f.Add([]byte(`{"metadata":{"annotations":{"kubecenter.io/eso-stale-after-minutes":"-5"}}}`))
	f.Add([]byte(`{"metadata":{"annotations":{"kubecenter.io/eso-stale-after-minutes":"abc"}}}`))
	f.Add([]byte(`{"metadata":{"annotations":{"kubecenter.io/eso-alert-on-recovery":"maybe"}}}`))

	// very large/deeply nested structure
	f.Add([]byte(`{"spec":{"namespaceSelectors":[{"matchLabels":{"a":"b","c":"d"}},{"matchExpressions":[{"key":"env","operator":"In","values":["prod"]}]}]}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		u, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}
		_ = normalizeExternalSecret(u)
		_ = normalizeClusterExternalSecret(u)
		_ = normalizeSecretStore(u, "Namespaced")
		_ = normalizeSecretStore(u, "Cluster")
		_ = normalizePushSecret(u)
	})
}
