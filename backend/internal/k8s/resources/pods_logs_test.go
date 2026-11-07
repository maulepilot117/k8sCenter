package resources

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	k8s "github.com/kubecenter/kubecenter/internal/k8s"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// noPreviousStatus is the API server's answer to a previous=true log request
// for a container that never restarted.
const noPreviousStatus = `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"previous terminated container \"app\" in pod \"web-0\" not found","reason":"BadRequest","code":400}`

// logsHandler routes the typed client to a stub API server that answers every
// request with status and body.
func logsHandler(t *testing.T, status int, body string) *Handler {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Handler{
		ClusterRouter: k8s.NewClusterRouter(k8s.NewFakeClientFactory(cs), nil, "", logger),
		AccessChecker: NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
	}
}

func podLogsRequest(query string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/resources/pods/apps/web-0/logs?"+query, nil)
	user := &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}}
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "apps")
	rctx.URLParams.Add("name", "web-0")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestHandlePodLogs_NoPreviousInstance(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		wantStatus int
		wantMsg    string
	}{
		{"previous names the missing instance", "container=app&previous=true", http.StatusNotFound, NoPreviousContainerMessage},
		{"current keeps the generic mapping", "container=app&previous=false", http.StatusBadRequest, "kubernetes API error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := logsHandler(t, http.StatusBadRequest, noPreviousStatus)
			w := httptest.NewRecorder()
			h.HandlePodLogs(w, podLogsRequest(tc.query))

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			var body struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v: %s", err, w.Body.String())
			}
			if body.Error.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", body.Error.Message, tc.wantMsg)
			}
		})
	}
}
