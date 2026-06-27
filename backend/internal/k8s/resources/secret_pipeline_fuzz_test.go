package resources

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	k8s "github.com/kubecenter/kubecenter/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// pipelineSentinel is the plaintext seeded into every fuzzed Secret's Data,
// StringData, and last-applied-configuration annotation. The leak oracle
// asserts it NEVER survives a List/Get response — in plaintext (StringData,
// annotation) OR base64 (the JSON encoding of the []byte Data values). It is
// long and distinctive so a coverage-guided fuzzer (which has never seen these
// bytes as input — the constant lives only in test code, never in a seed) will
// not reproduce it in a non-masked field (name, key, annotation key) and cause
// a false positive.
const pipelineSentinel = "p1pel1ne-SENTINEL-must-never-leak-7f3a9c"

// FuzzSecretPipeline drives the *full* Secret HTTP pipeline — handler →
// ClusterRouter → ClientFactory(fake) → maskedSecret → JSON encode — rather
// than the pure maskedSecret function alone (covered by FuzzMaskedSecret).
// Secrets are fetched on-demand via the impersonated typed client (NOT the
// informer cache), which is why this needs the ClientFactory → kubernetes.Interface
// seam: a fake clientset seeded with adversarial Secrets is injected through it.
//
// Oracles:
//   - A (crash-safety): no input — arbitrary keys, binary values, odd
//     namespace/name/reveal-key — panics any of HandleListSecrets,
//     HandleGetSecret, or HandleRevealSecret. A panic fails the fuzz.
//   - D (secret-leak masking): the seeded sentinel never appears, plaintext or
//     base64, in a List or Get response body. Reveal is the sanctioned
//     plaintext path (explicit user action, audit-logged) and is therefore
//     exempt from the leak oracle — crash-safety only.
func FuzzSecretPipeline(f *testing.F) {
	b64Sentinel := base64.StdEncoding.EncodeToString([]byte(pipelineSentinel))

	// Seed corpus: realistic + adversarial "teeth". Every seed routes a Secret
	// carrying the sentinel through the masking path, so each WOULD fail if
	// maskedSecret stopped masking (verified by mutation).
	f.Add("default", "db-creds", "password", []byte("hunter2"), "team", "password")
	f.Add("", "", "", []byte(""), "", "")
	f.Add("kube-system", "tls", "tls.crt", []byte{0x00, 0xFF, 0x1B, 0x0A}, "owner", "missing-key")
	f.Add("ns", "big", "blob", []byte(strings.Repeat("A", 8192)), "ann", "blob")
	// fuzzKey collides with the reserved sentinel key — exercise the guard.
	f.Add("default", "creds", "__sentinel__", []byte("x"), "y", "__sentinel__")

	f.Fuzz(func(t *testing.T, ns, name, fuzzKey string, fuzzVal []byte, annKey, revealKey string) {
		// Normalize identity so List/Get actually hit the seeded Secret — an
		// empty/invalid name would 404 and make the leak oracle vacuous (no
		// teeth). The fuzzed ns/name still vary the stored object's identity.
		if ns == "" {
			ns = "default"
		}
		if name == "" {
			name = "s"
		}

		secret := seedFuzzSecret(ns, name, fuzzKey, fuzzVal, annKey)
		h := secretFuzzHandler(fake.NewSimpleClientset(secret))

		assertNoLeak := func(op, body string) {
			if strings.Contains(body, pipelineSentinel) {
				t.Fatalf("%s leaked plaintext sentinel in response: %s", op, body)
			}
			if strings.Contains(body, b64Sentinel) {
				t.Fatalf("%s leaked base64 sentinel in response: %s", op, body)
			}
		}

		// List — both the namespace-scoped path (hits the secret) and the
		// all-namespaces path (ns=""). Both must return 200 carrying the masked
		// secret. Asserting the status keeps the leak oracle non-vacuous: a
		// future regression that short-circuits to an empty error body (e.g. a
		// 500 before maskedSecret runs) would otherwise satisfy assertNoLeak
		// trivially. The namespaced list always contains the seeded secret, so
		// masking is genuinely exercised regardless of the fake's cross-namespace
		// listing behavior.
		for _, listNS := range []string{ns, ""} {
			rr := httptest.NewRecorder()
			h.HandleListSecrets(rr, secretReq(map[string]string{"namespace": listNS}))
			if rr.Code != http.StatusOK {
				t.Fatalf("list (ns=%q) returned %d, want 200: %s", listNS, rr.Code, rr.Body.String())
			}
			assertNoLeak("list", rr.Body.String())
		}

		// Get the specific secret — must hit the seeded object (200) so the leak
		// oracle exercises masking rather than passing on a 404 body.
		{
			rr := httptest.NewRecorder()
			h.HandleGetSecret(rr, secretReq(map[string]string{"namespace": ns, "name": name}))
			if rr.Code != http.StatusOK {
				t.Fatalf("get (%s/%s) returned %d, want 200: %s", ns, name, rr.Code, rr.Body.String())
			}
			assertNoLeak("get", rr.Body.String())
		}

		// Reveal — crash-safety only; reveal deliberately returns the plaintext
		// (base64-encoded) value, so the leak oracle does not apply.
		{
			rr := httptest.NewRecorder()
			h.HandleRevealSecret(rr, secretReq(map[string]string{"namespace": ns, "name": name, "key": revealKey}))
			_ = rr.Body.String()
		}
	})
}

// seedFuzzSecret builds a Secret whose sensitive fields all carry the sentinel
// (so masking has something to erase) while keys, the extra annotation key, and
// the secret's identity come from fuzz input. The non-sensitive "keep" annotation
// holds a FIXED value, never the sentinel, so an un-stripped non-sensitive
// annotation cannot itself trip the leak oracle.
func seedFuzzSecret(ns, name, fuzzKey string, fuzzVal []byte, annKey string) *corev1.Secret {
	const sentinelKey = "__sentinel__"
	data := map[string][]byte{sentinelKey: []byte(pipelineSentinel)}
	if fuzzKey != "" && fuzzKey != sentinelKey {
		data[fuzzKey] = fuzzVal
	}

	annotations := map[string]string{
		// MUST be stripped by masking — carries the sentinel via a kubectl manifest.
		lastAppliedConfigAnnotation: `{"apiVersion":"v1","kind":"Secret","stringData":{"password":"` + pipelineSentinel + `"}}`,
	}
	if annKey != "" && annKey != lastAppliedConfigAnnotation {
		annotations[annKey] = "non-sensitive-fixed"
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: annotations},
		Data:       data,
		StringData: map[string]string{"str-sentinel": pipelineSentinel},
	}
}

// secretFuzzHandler builds a Handler whose typed-client path is routed to fakeCS
// and whose RBAC gate trivially allows, so the fuzz reaches the masking path.
func secretFuzzHandler(fakeCS *fake.Clientset) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := k8s.NewFakeClientFactory(fakeCS)
	return &Handler{
		ClusterRouter: k8s.NewClusterRouter(factory, nil, "", logger),
		AccessChecker: NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
	}
}

// secretReq builds an authenticated request carrying the given chi URL params.
func secretReq(params map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/resources/secrets", nil)
	user := &auth.User{
		Username:           "admin",
		KubernetesUsername: "admin",
		KubernetesGroups:   []string{"system:masters"},
	}
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
