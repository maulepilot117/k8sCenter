package resources

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// TestHandlePodExec_RemoteCluster_Returns501 verifies that HandlePodExec
// returns HTTP 501 for requests targeting a non-local cluster (Finding P2-5).
// The gate fires before the WebSocket upgrade so the error is delivered as a
// plain HTTP response, not a WebSocket close frame.
func TestHandlePodExec_RemoteCluster_Returns501(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	h := &Handler{
		AccessChecker: NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
		TaskManager:   NewTaskManager(),
	}

	req := httptest.NewRequest(http.MethodGet, "/ws/exec/default/my-pod/app", nil)

	// Build chi route context with URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	rctx.URLParams.Add("name", "my-pod")
	rctx.URLParams.Add("container", "app")

	// Inject authenticated user and non-local cluster ID, then chi context
	user := &auth.User{
		ID:                 "u1",
		Username:           "alice",
		KubernetesUsername: "alice",
		KubernetesGroups:   []string{"dev"},
		Roles:              []string{"admin"},
	}
	ctx := auth.ContextWithUser(req.Context(), user)
	ctx = middleware.WithClusterID(ctx, "remote-cluster-1")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.HandlePodExec(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Errorf("HandlePodExec with remote clusterID: got status %d, want %d (body: %s)",
			rr.Code, http.StatusNotImplemented, rr.Body.String())
	}
}

// TestHandlePodExec_LocalCluster_ProceedsToUpgrade verifies that HandlePodExec
// does NOT return 501 for local cluster requests — the request proceeds past
// the cluster gate and fails only at the WebSocket upgrade step (since the
// test uses a plain httptest recorder, not a real WS connection).
func TestHandlePodExec_LocalCluster_ProceedsToUpgrade(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	h := &Handler{
		AccessChecker: NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
		TaskManager:   NewTaskManager(),
		// OriginValidator nil → rejects WS upgrade with 403 (origin check)
	}

	req := httptest.NewRequest(http.MethodGet, "/ws/exec/default/my-pod/app", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	rctx.URLParams.Add("name", "my-pod")
	rctx.URLParams.Add("container", "app")

	user := &auth.User{
		ID:                 "u1",
		Username:           "alice",
		KubernetesUsername: "alice",
		KubernetesGroups:   []string{"dev"},
		Roles:              []string{"admin"},
	}
	ctx := auth.ContextWithUser(req.Context(), user)
	ctx = middleware.WithClusterID(ctx, "local") // local cluster
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.HandlePodExec(rr, req)

	// Must NOT be 501 — the local cluster gate was passed.
	if rr.Code == http.StatusNotImplemented {
		t.Errorf("HandlePodExec with local clusterID should not return 501, got %d", rr.Code)
	}
}
