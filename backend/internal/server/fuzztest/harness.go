// Package fuzztest provides a reusable test harness for Layer B HTTP-handler
// fuzzing of the k8sCenter backend. It builds a real [server.Server] backed by
// fake k8s clients and in-memory auth stores so fuzz targets can drive the full
// middleware chain (Auth → CSRF → ClusterContext → handler) without a real
// cluster or database.
//
// # Two server modes
//
//   - Auth-only (Opts.WithResources == false): mirrors the existing testServer
//     helper in handle_auth_test.go. K8sClient and Informers are nil so
//     ResourceHandler is not constructed. Use for oracle C (auth/authz) targets.
//
//   - WithResources (Opts.WithResources == true): additionally constructs a
//     [k8s.ClientFactory] via [k8s.NewTestClientFactoryWithDynamic] backed by a
//     zero-value *kubernetes.Clientset stub, plus a [k8s.InformerManager] backed
//     by a fake clientset. A [k8s.ClusterRouter] is wired with no ClusterStore
//     (local-only). An [resources.NewAlwaysAllowAccessChecker] is used so handler
//     RBAC checks pass without a real API server.
//
// # OQ1 resolution — informer wiring for WithResources mode
//
// The plan anticipated that Secrets might use the dynamic-client path and could
// be seeded via dynamicfake. After reading resources/secrets.go, the truth is:
//
//   - Secrets are fetched on-demand via resources.Handler.impersonatingClient →
//     ClusterRouter.ClientForCluster → ClientFactory.ClientForUser.
//     ClientForUser returns *kubernetes.Clientset (concrete type).
//   - Secrets are intentionally NOT cached in the informer (see informers.go
//     comment). The InformerManager is never consulted for Secret reads.
//   - The fake k8s clientset (k8s.io/client-go/kubernetes/fake) is NOT the same
//     type as *kubernetes.Clientset, so it cannot be passed to NewTestClientFactory.
//
// Consequence for WithResources mode: the harness wires a zero-value
// *kubernetes.Clientset stub (same as certmanager/adapter_test.go does) so that
// server.New constructs ResourceHandler and registers routes. Actual Secret GET
// calls through the full handler chain will fail at the API level (stub has no
// backing store) — this is acceptable for U1 route-registration smoke tests.
//
// For oracle D (U3) — Secret masking end-to-end: build resources.Handler
// directly with fake.NewSimpleClientset (like resources_test.go does) and call
// HandleGetSecret directly, OR add a new ClientFactory seam that accepts
// kubernetes.Interface (requires a production-file change — flagged as a
// U3 decision).
//
// Wiring chosen: (b) — non-nil InformerManager satisfies server.New's guard;
// InformerManager is NOT started (no Start/WaitForSync call) since no informer
// lister is exercised by the Secret path.
//
// # OQ4 resolution — dynamicfake GVR/scheme registration
//
// For routes that use the dynamic client path (CRD resources), a
// dynamicfake.NewSimpleDynamicClientWithCustomListKinds call with explicit GVR
// and list-kind registration is needed. For U1 (typed-client-only resources),
// the dynamic override is nil (safe: only dynamic-client routes would use it).
// See backend/internal/scanning/trivy_test.go for the registration pattern.
package fuzztest

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// Opts controls how NewServer builds the test server.
type Opts struct {
	// WithResources enables k8s resource routes (ResourceHandler).
	// When false the server has auth routes only — suitable for oracle C targets.
	WithResources bool

	// Secrets is accepted for API compatibility with future U3 work. Currently
	// unused in server construction — see OQ1 note in the package doc.
	Secrets []*corev1.Secret
}

// NewServer returns a fully wired *server.Server for use in fuzz tests.
// The returned server's Router can be driven directly via ServeHTTP.
//
// Call t.Helper() at the fuzz target's entry point before passing t here; the
// helper chain will produce clean test output on failure.
func NewServer(t testing.TB, opts Opts) *server.Server {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		Dev:       true,
		ClusterID: "test-cluster",
		Server: config.ServerConfig{
			Port:            8080,
			RequestTimeout:  config.DefaultRequestTimeout,
			ShutdownTimeout: config.DefaultShutdownTimeout,
		},
		Log: config.LogConfig{Level: "error", Format: "json"},
	}

	tokenManager := auth.NewTokenManager(testSigningKey)
	localAuth := auth.NewLocalProvider(auth.NewMemoryUserStore(), logger)
	sessions := auth.NewSessionStore()
	auditLogger := audit.NewSlogLogger(logger)
	rateLimiter := middleware.NewRateLimiter()

	authRegistry := auth.NewProviderRegistry()
	authRegistry.RegisterCredential("local", "Local Accounts", localAuth)

	deps := server.Deps{
		Config:         cfg,
		Logger:         logger,
		TokenManager:   tokenManager,
		LocalAuth:      localAuth,
		AuthRegistry:   authRegistry,
		OIDCStateStore: auth.NewOIDCStateStore(),
		Sessions:       sessions,
		AuditLogger:    auditLogger,
		RateLimiter:    rateLimiter,
		ReadyFn:        func() bool { return true },
	}

	if opts.WithResources {
		// Stub typed clientset: a zero-value *kubernetes.Clientset satisfies the
		// NewTestClientFactoryWithDynamic parameter. ClientForUser will return this
		// stub; actual k8s API calls from handlers will fail (no backing store),
		// which is expected — U1 only verifies route registration, not data fetch.
		stubCS := &kubernetes.Clientset{}

		// ClientFactory with testOverride set: ClientForUser returns stubCS directly.
		// Dynamic override is nil — no dynamic-client routes are exercised in U1.
		clientFactory := k8s.NewTestClientFactoryWithDynamic(stubCS, nil)

		// Build a separate fake clientset for the InformerManager (informers are
		// satisfied by a fake that supports List/Watch on core v1 types).
		fakeForInformers := buildFakeClientset(opts.Secrets)

		// InformerManager: non-nil satisfies server.New's guard; Secret reads
		// never touch informer listers (OQ1 wiring b). Not started — no
		// lister is exercised by U1 smoke tests.
		informers := k8s.NewInformerManager(fakeForInformers, nil, logger)

		// ClusterRouter: nil ClusterStore means local-only (no remote clusters).
		clusterRouter := k8s.NewClusterRouter(clientFactory, nil, "", logger)

		// AlwaysAllow so handler RBAC checks pass; oracle D asserts masking, not RBAC.
		accessChecker := resources.NewAlwaysAllowAccessChecker()
		accessChecker.SetClusterRouter(clusterRouter)

		deps.K8sClient = clientFactory
		deps.Informers = informers
		deps.ClusterRouter = clusterRouter
		deps.AccessChecker = accessChecker
	}

	return server.New(deps)
}

// buildFakeClientset constructs a fake.Clientset pre-populated with secrets.
// Returns kubernetes.Interface so it can be passed to NewInformerManager.
func buildFakeClientset(secrets []*corev1.Secret) *fake.Clientset {
	objs := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		objs = append(objs, s.DeepCopy())
	}
	return fake.NewSimpleClientset(objs...)
}

// SeedSecret is a convenience constructor for a v1.Secret suitable for use in
// Opts.Secrets. The data map values are the raw bytes (not base64-encoded) —
// the fake clientset stores them as-is and the Secret GET handler masks them
// before returning to the caller.
func SeedSecret(namespace, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}

// CreateAdminUser registers an admin user in the server's LocalAuth store and
// returns the minted access token. Useful for smoke tests that need an authed
// request without going through the full login flow.
func CreateAdminUser(t testing.TB, srv *server.Server, username, password string) string {
	t.Helper()

	_, err := srv.LocalAuth.CreateUser(context.Background(), username, password, []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("fuzztest.CreateAdminUser: CreateUser(%q): %v", username, err)
	}

	user := &auth.User{
		ID:                 username,
		Username:           username,
		Provider:           "local",
		KubernetesUsername: "system:serviceaccount:default:" + username,
		KubernetesGroups:   []string{"system:masters"},
		Roles:              []string{"admin"},
	}

	tok, err := srv.TokenManager.IssueAccessToken(user)
	if err != nil {
		t.Fatalf("fuzztest.CreateAdminUser: IssueAccessToken: %v", err)
	}
	return tok
}

// testSigningKey is the HMAC-SHA256 key used for all tokens in the harness.
// Kept package-private; tokens.go uses this via direct reference.
var testSigningKey = []byte("fuzztest-signing-key-minimum-32b")
