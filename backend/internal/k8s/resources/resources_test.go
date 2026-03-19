package resources

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	k8s "github.com/kubecenter/kubecenter/internal/k8s"
)

// testHandler creates a Handler with a fake k8s client and pre-loaded informer cache.
func testHandler(t *testing.T, objs ...runtime.Object) (*Handler, *fake.Clientset) {
	t.Helper()

	fakeCS := fake.NewSimpleClientset(objs...)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	im := k8s.NewInformerManager(fakeCS, nil, fakeCS.Discovery(), logger)
	im.Start(ctx)
	if err := im.WaitForSync(ctx); err != nil {
		t.Fatalf("informer sync failed: %v", err)
	}

	h := &Handler{
		Informers:     im,
		AccessChecker: NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
		TaskManager:   NewTaskManager(),
	}

	return h, fakeCS
}


// requestWithUser creates an HTTP request with an authenticated user in context.
func requestWithUser(method, path string, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Content-Type", "application/json")

	user := &auth.User{
		ID:                 "test-user-id",
		Username:           "admin",
		Provider:           "local",
		KubernetesUsername: "admin",
		KubernetesGroups:   []string{"system:masters"},
		Roles:              []string{"admin"},
	}
	ctx := auth.ContextWithUser(req.Context(), user)
	return req.WithContext(ctx)
}

// decodeResponse decodes a JSON API response.
func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) api.Response {
	t.Helper()
	var resp api.Response
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v (body: %s)", err, rr.Body.String())
	}
	return resp
}

// --- Tests ---

func TestListDeployments(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
			Labels:    map[string]string{"app": "nginx"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}},
			},
		},
	}

	h, _ := testHandler(t, dep)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments/default", "")

	// Set chi URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListDeployments(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeResponse(t, rr)
	if resp.Metadata == nil || resp.Metadata.Total != 1 {
		t.Fatalf("expected 1 deployment, got metadata: %+v", resp.Metadata)
	}
}

func TestListDeployments_AllNamespaces(t *testing.T) {
	deps := []runtime.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx"}}},
				},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "redis", Namespace: "staging"},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "redis"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "redis"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "redis", Image: "redis"}}},
				},
			},
		},
	}

	h, _ := testHandler(t, deps...)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments", "")

	rctx := chi.NewRouteContext()
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListDeployments(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeResponse(t, rr)
	if resp.Metadata == nil || resp.Metadata.Total != 2 {
		t.Fatalf("expected 2 deployments, got: %+v", resp.Metadata)
	}
}

func TestListDeployments_LabelSelector(t *testing.T) {
	deps := []runtime.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx"}}},
				},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "redis", Namespace: "default", Labels: map[string]string{"app": "redis"}},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "redis"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "redis"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "redis", Image: "redis"}}},
				},
			},
		},
	}

	h, _ := testHandler(t, deps...)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments/default?labelSelector=app%3Dnginx", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListDeployments(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeResponse(t, rr)
	if resp.Metadata == nil || resp.Metadata.Total != 1 {
		t.Fatalf("expected 1 deployment with label app=nginx, got: %+v", resp.Metadata)
	}
}

func TestPagination(t *testing.T) {
	var deps []runtime.Object
	for i := 0; i < 5; i++ {
		name := "dep-" + strconv.Itoa(i)
		deps = append(deps, &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: name, Image: "nginx"}}},
				},
			},
		})
	}

	h, _ := testHandler(t, deps...)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments/default?limit=2", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListDeployments(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	if resp.Metadata.Total != 5 {
		t.Errorf("expected total=5, got %d", resp.Metadata.Total)
	}
	if resp.Metadata.Continue == "" {
		t.Error("expected continue token for page 1 of 5 items with limit 2")
	}
}

func TestListPods(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx-abc123", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}},
	}

	h, _ := testHandler(t, pod)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/pods/default", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListPods(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Metadata.Total != 1 {
		t.Fatalf("expected 1 pod, got %d", resp.Metadata.Total)
	}
}

func TestGetDeployment(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx"}}},
			},
		},
	}

	h, _ := testHandler(t, dep)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments/default/nginx", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	rctx.URLParams.Add("name", "nginx")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetDeployment(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetDeployment_NotFound(t *testing.T) {
	h, _ := testHandler(t)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments/default/nonexistent", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	rctx.URLParams.Add("name", "nonexistent")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetDeployment(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSecretMasking(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "default"},
		Data: map[string][]byte{
			"password": []byte("supersecret"),
			"username": []byte("admin"),
		},
	}

	masked := maskedSecret(secret)
	for k, v := range masked.Data {
		if string(v) != "****" {
			t.Errorf("expected key %q to be masked, got %q", k, string(v))
		}
	}

	// Verify original is untouched
	if string(secret.Data["password"]) != "supersecret" {
		t.Error("maskedSecret modified the original")
	}
}

func TestSecretMaskingStripsLastAppliedAnnotation(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "db-creds",
			Namespace: "default",
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"db-creds"},"stringData":{"password":"supersecret"}}`,
				"some-other-annotation": "keep-me",
			},
		},
		Data: map[string][]byte{
			"password": []byte("supersecret"),
		},
	}

	masked := maskedSecret(secret)

	// last-applied-configuration must be stripped
	if _, exists := masked.Annotations["kubectl.kubernetes.io/last-applied-configuration"]; exists {
		t.Error("maskedSecret did not strip kubectl.kubernetes.io/last-applied-configuration annotation")
	}

	// Other annotations must be preserved
	if masked.Annotations["some-other-annotation"] != "keep-me" {
		t.Error("maskedSecret incorrectly removed non-sensitive annotation")
	}

	// Data must still be masked
	if string(masked.Data["password"]) != "****" {
		t.Errorf("expected password to be masked, got %q", string(masked.Data["password"]))
	}

	// Original must be untouched
	if _, exists := secret.Annotations["kubectl.kubernetes.io/last-applied-configuration"]; !exists {
		t.Error("maskedSecret modified the original secret's annotations")
	}
}

func TestListNodes(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status:     corev1.NodeStatus{Phase: corev1.NodeRunning},
	}

	h, _ := testHandler(t, node)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/nodes", "")

	rctx := chi.NewRouteContext()
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListNodes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Metadata.Total != 1 {
		t.Fatalf("expected 1 node, got %d", resp.Metadata.Total)
	}
}

func TestTaskManager(t *testing.T) {
	// Override timeNow for predictable test
	origTimeNow := timeNow
	timeNow = func() time.Time { return time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC) }
	defer func() { timeNow = origTimeNow }()

	tm := NewTaskManager()

	id := tm.Create("drain", "worker-1", "", "admin")
	if id == "" {
		t.Fatal("expected non-empty task ID")
	}

	task, ok := tm.Get(id)
	if !ok {
		t.Fatal("expected to find task")
	}
	if task.Status != TaskStatusPending {
		t.Errorf("expected pending, got %s", task.Status)
	}

	tm.UpdateStatus(id, TaskStatusRunning, "draining", 50)
	task, _ = tm.Get(id)
	if task.Status != TaskStatusRunning {
		t.Errorf("expected running, got %s", task.Status)
	}
	if task.Progress != 50 {
		t.Errorf("expected progress 50, got %d", task.Progress)
	}

	tm.UpdateStatus(id, TaskStatusComplete, "done", 100)
	task, _ = tm.Get(id)
	if task.Status != TaskStatusComplete {
		t.Errorf("expected complete, got %s", task.Status)
	}
	if task.EndedAt == nil {
		t.Error("expected EndedAt to be set")
	}
}

func TestUnauthenticatedRequest(t *testing.T) {
	h, _ := testHandler(t)
	rr := httptest.NewRecorder()
	// Request WITHOUT user in context
	req := httptest.NewRequest("GET", "/api/v1/resources/pods/default", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListPods(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListNamespaces(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
	}
	h, _ := testHandler(t, ns)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/namespaces", "")

	rctx := chi.NewRouteContext()
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListNamespaces(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Metadata.Total != 1 {
		t.Fatalf("expected 1 namespace, got %d", resp.Metadata.Total)
	}
}

func TestListServices(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}},
	}
	h, _ := testHandler(t, svc)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/services/default", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListServices(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Metadata.Total != 1 {
		t.Fatalf("expected 1 service, got %d", resp.Metadata.Total)
	}
}

func TestErrorMapping(t *testing.T) {
	// Test that mapK8sError returns proper status codes for k8s errors
	rr := httptest.NewRecorder()

	// Simulate not found via informer lister (returns cache.ErrNotFound type)
	h, _ := testHandler(t) // empty - no deployments
	req := requestWithUser("GET", "/api/v1/resources/deployments/default/missing", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	rctx.URLParams.Add("name", "missing")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetDeployment(rr, req)

	// Informer lister returns a not-found error
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing deployment, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAccessDenied_List(t *testing.T) {
	h, _ := testHandler(t)
	h.AccessChecker = NewAlwaysDenyAccessChecker()

	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments/default", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListDeployments(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAccessDenied_Get(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx"}}},
			},
		},
	}

	h, _ := testHandler(t, dep)
	h.AccessChecker = NewAlwaysDenyAccessChecker()

	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments/default/nginx", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	rctx.URLParams.Add("name", "nginx")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetDeployment(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInvalidLabelSelector(t *testing.T) {
	h, _ := testHandler(t)
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/resources/deployments/default?labelSelector=!!!invalid", "")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "default")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleListDeployments(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid label selector, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestValidateK8sName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"empty", "", true},
		{"simple", "nginx", true},
		{"with-dashes", "my-deployment", true},
		{"with-dots", "my.deployment", true},
		{"with-numbers", "nginx-123", true},
		{"max-length", strings.Repeat("a", 253), true},
		{"too-long", strings.Repeat("a", 254), false},
		{"starts-with-dash", "-nginx", false},
		{"ends-with-dash", "nginx-", false},
		{"uppercase", "Nginx", false},
		{"spaces", "my deployment", false},
		{"special-chars", "nginx@latest", false},
		{"slashes", "../etc/passwd", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateK8sName(tt.input); got != tt.valid {
				t.Errorf("ValidateK8sName(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		})
	}
}

func TestHasActiveTask(t *testing.T) {
	tm := NewTaskManager()
	id := tm.Create("drain", "worker-1", "", "admin")

	if !tm.HasActiveTask("drain", "worker-1") {
		t.Error("expected active task for drain/worker-1")
	}
	if tm.HasActiveTask("drain", "worker-2") {
		t.Error("did not expect active task for drain/worker-2")
	}

	tm.UpdateStatus(id, TaskStatusComplete, "done", 100)
	if tm.HasActiveTask("drain", "worker-1") {
		t.Error("completed task should not count as active")
	}
}
