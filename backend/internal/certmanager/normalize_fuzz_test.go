package certmanager

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

// FuzzCertManagerNormalizers asserts that every cert-manager normalizer is
// crash-safe on arbitrary/adversarial unstructured input. A normalizer panic
// here is a real reliability bug: normalizeCertificate/normalizeIssuer run in
// the certmanager expiry poller (poller.go), a BACKGROUND goroutine NOT behind
// chi's panic-recovery middleware, so a panic there crashes the process rather
// than returning a 500. Oracle: no panic. Returned errors/zero-values are fine.
func FuzzCertManagerNormalizers(f *testing.F) {
	// ── Structural teeth that reproduce the two production panics ──────────
	// Bug 1 (normalizeCertRequest line 275): u.Object["metadata"].(map[string]any)
	// is unguarded — panics when metadata is absent or the wrong type.
	f.Add([]byte(`{}`)) // no metadata key → nil → panic on unguarded assertion

	// Bug 2 (normalizeOrder line 305): obj["metadata"].(map[string]any) is also
	// unguarded inside the ownerReferences chain — panics when metadata is absent
	// or a non-map type.
	f.Add([]byte(`{"metadata":"oops"}`))  // metadata as string
	f.Add([]byte(`{"metadata":[]}`))      // metadata as list
	f.Add([]byte(`{"spec":[],"status":"x"}`))              // spec/status wrong types
	f.Add([]byte(`{"metadata":{"creationTimestamp":12345}}`)) // timestamp wrong type
	f.Add([]byte(`{"metadata":{"name":"x"},"status":{"conditions":{"type":"Ready"}}}`)) // conditions as map

	// ── Realistic Certificate (cert-manager.io/v1) ─────────────────────────
	f.Add([]byte(`{
		"apiVersion": "cert-manager.io/v1",
		"kind": "Certificate",
		"metadata": {
			"name": "web-tls",
			"namespace": "production",
			"uid": "abc-123",
			"creationTimestamp": "2024-01-15T10:00:00Z",
			"annotations": {
				"kubecenter.io/cert-warn-threshold-days": "30",
				"kubecenter.io/cert-critical-threshold-days": "7"
			}
		},
		"spec": {
			"secretName": "web-tls",
			"dnsNames": ["example.com", "www.example.com"],
			"issuerRef": {
				"name": "letsencrypt-prod",
				"kind": "ClusterIssuer",
				"group": "cert-manager.io"
			},
			"duration": "2160h",
			"renewBefore": "360h"
		},
		"status": {
			"notBefore": "2024-01-15T10:00:00Z",
			"notAfter": "2024-04-15T10:00:00Z",
			"renewalTime": "2024-04-09T10:00:00Z",
			"conditions": [
				{
					"type": "Ready",
					"status": "True",
					"reason": "Ready",
					"message": "Certificate is up to date and has not expired"
				}
			]
		}
	}`))

	// ── Realistic Issuer (cert-manager.io/v1) ──────────────────────────────
	f.Add([]byte(`{
		"apiVersion": "cert-manager.io/v1",
		"kind": "Issuer",
		"metadata": {
			"name": "letsencrypt-staging",
			"namespace": "production",
			"uid": "issuer-uid-1",
			"creationTimestamp": "2024-01-10T08:00:00Z"
		},
		"spec": {
			"acme": {
				"email": "admin@example.com",
				"server": "https://acme-staging-v02.api.letsencrypt.org/directory",
				"privateKeySecretRef": {"name": "letsencrypt-staging-key"},
				"solvers": [{"http01": {"ingress": {"class": "nginx"}}}]
			}
		},
		"status": {
			"conditions": [
				{
					"type": "Ready",
					"status": "True",
					"reason": "ACMEAccountRegistered",
					"message": "The ACME account was registered",
					"lastTransitionTime": "2024-01-10T08:05:00Z"
				}
			]
		}
	}`))

	// ── Realistic CertificateRequest (cert-manager.io/v1) ─────────────────
	f.Add([]byte(`{
		"apiVersion": "cert-manager.io/v1",
		"kind": "CertificateRequest",
		"metadata": {
			"name": "web-tls-abc12",
			"namespace": "production",
			"uid": "cr-uid-123",
			"creationTimestamp": "2024-01-15T10:00:00Z"
		},
		"spec": {
			"issuerRef": {
				"name": "letsencrypt-prod",
				"kind": "ClusterIssuer",
				"group": "cert-manager.io"
			},
			"request": "LS0tLS1CRUdJTi..."
		},
		"status": {
			"conditions": [
				{
					"type": "Ready",
					"status": "True",
					"reason": "CertificateIssued",
					"message": "Certificate fetched from issuer successfully"
				}
			],
			"completionTime": "2024-01-15T10:01:00Z"
		}
	}`))

	// ── Realistic Order (acme.cert-manager.io/v1) ─────────────────────────
	f.Add([]byte(`{
		"apiVersion": "acme.cert-manager.io/v1",
		"kind": "Order",
		"metadata": {
			"name": "web-tls-abc12-12345",
			"namespace": "production",
			"uid": "order-uid-123",
			"creationTimestamp": "2024-01-15T10:00:00Z",
			"ownerReferences": [
				{
					"name": "web-tls-abc12",
					"kind": "CertificateRequest",
					"apiVersion": "cert-manager.io/v1",
					"uid": "cr-uid-123"
				}
			]
		},
		"status": {
			"state": "valid",
			"url": "https://acme-v02.api.letsencrypt.org/acme/order/123"
		}
	}`))

	// ── Realistic Challenge (acme.cert-manager.io/v1) ─────────────────────
	f.Add([]byte(`{
		"apiVersion": "acme.cert-manager.io/v1",
		"kind": "Challenge",
		"metadata": {
			"name": "web-tls-abc12-12345-1234567890",
			"namespace": "production",
			"uid": "challenge-uid-123",
			"creationTimestamp": "2024-01-15T10:00:00Z",
			"ownerReferences": [
				{
					"name": "web-tls-abc12-12345",
					"kind": "Order",
					"apiVersion": "acme.cert-manager.io/v1",
					"uid": "order-uid-123"
				}
			]
		},
		"spec": {
			"type": "HTTP-01",
			"dnsName": "example.com",
			"token": "token123",
			"key": "key456"
		},
		"status": {
			"state": "valid",
			"reason": "Challenge completed successfully"
		}
	}`))

	// ── Additional adversarial shapes ──────────────────────────────────────
	f.Add([]byte(`{"metadata":{"name":null,"namespace":null}}`))
	f.Add([]byte(`{"metadata":{"ownerReferences":"not-a-list"}}`))
	f.Add([]byte(`{"metadata":{"ownerReferences":[{"kind":42}]}}`))
	f.Add([]byte(`{"status":{"conditions":[{"type":"Ready","status":"True"},null]}}`))
	f.Add([]byte(`{"spec":{"issuerRef":null}}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		u, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}
		_, _ = normalizeCertificate(u)
		_ = normalizeIssuer(u, "Namespaced")
		_ = normalizeIssuer(u, "Cluster")
		_ = normalizeCertRequest(u)
		_ = normalizeOrder(u)
		_ = normalizeChallenge(u)
	})
}
