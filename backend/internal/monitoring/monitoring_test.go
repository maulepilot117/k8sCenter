package monitoring

import (
	"embed"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kubecenter/kubecenter/internal/monitoring/dashboards"
)

// --- QueryTemplate.Render tests ---

func TestRenderTemplate_Success(t *testing.T) {
	tmpl := QueryTemplate{
		Name:      "test",
		Query:     `up{namespace="$namespace",pod="$pod"}`,
		Variables: []string{"namespace", "pod"},
	}
	result, err := tmpl.Render(map[string]string{"namespace": "default", "pod": "my-pod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `up{namespace="default",pod="my-pod"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestRenderTemplate_MissingVariable(t *testing.T) {
	tmpl := QueryTemplate{
		Name:      "test",
		Query:     `up{namespace="$namespace"}`,
		Variables: []string{"namespace"},
	}
	_, err := tmpl.Render(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing variable")
	}
}

func TestRenderTemplate_InvalidValue(t *testing.T) {
	tmpl := QueryTemplate{
		Name:      "test",
		Query:     `up{namespace="$namespace"}`,
		Variables: []string{"namespace"},
	}
	_, err := tmpl.Render(map[string]string{"namespace": `foo"bar`})
	if err == nil {
		t.Fatal("expected error for invalid value containing quote")
	}
}

func TestRenderTemplate_EmptyValue(t *testing.T) {
	tmpl := QueryTemplate{
		Name:      "test",
		Query:     `up{namespace="$namespace"}`,
		Variables: []string{"namespace"},
	}
	_, err := tmpl.Render(map[string]string{"namespace": ""})
	if err == nil {
		t.Fatal("expected error for empty value")
	}
}

// --- isValidK8sName tests ---

func TestIsValidK8sName_Valid(t *testing.T) {
	cases := []string{"default", "my-pod", "kube-system", "pod123", "a"}
	for _, c := range cases {
		if !isValidK8sName(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}
}

func TestIsValidK8sName_Invalid(t *testing.T) {
	cases := []string{"", "UPPER", "has space", `has"quote`, "has{brace}", "a/b"}
	for _, c := range cases {
		if isValidK8sName(c) {
			t.Errorf("expected %q to be invalid", c)
		}
	}
}

// --- Grafana proxy path validation tests ---

func TestGrafanaProxy_AllowedPaths(t *testing.T) {
	allowed := []string{
		"/d/kubecenter-pods/overview",
		"/d-solo/kubecenter-pods/panel?panelId=1",
		"/api/dashboards/uid/foo",
		"/api/folders/",
		"/api/search?tag=kubecenter",
		"/public/build/app.js",
	}

	for _, path := range allowed {
		isAllowed := false
		for _, prefix := range allowedGrafanaPathPrefixes {
			if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
				isAllowed = true
				break
			}
		}
		if !isAllowed {
			t.Errorf("path %q should be allowed", path)
		}
	}
}

func TestGrafanaProxy_BlockedPaths(t *testing.T) {
	blocked := []string{
		"/api/admin/users",
		"/api/users/1",
		"/api/org/",
		"/login",
		"/",
	}

	for _, path := range blocked {
		isAllowed := false
		for _, prefix := range allowedGrafanaPathPrefixes {
			if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
				isAllowed = true
				break
			}
		}
		if isAllowed {
			t.Errorf("path %q should be blocked", path)
		}
	}
}

func TestGrafanaProxy_PathTraversal(t *testing.T) {
	// Create a discoverer with a dummy proxy so path validation is reached
	d := &Discoverer{
		status:    &MonitoringStatus{},
		grafProxy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	cases := []struct {
		path       string
		wantStatus int
	}{
		{"/api/v1/monitoring/grafana/proxy/../../../etc/passwd", http.StatusForbidden},
		{"/api/v1/monitoring/grafana/proxy/%2e%2e/etc/passwd", http.StatusForbidden},
		{"/api/v1/monitoring/grafana/proxy/d/..%2f..%2fetc", http.StatusForbidden},
	}

	for _, tc := range cases {
		r := httptest.NewRequest("GET", tc.path, nil)
		w := httptest.NewRecorder()
		h.GrafanaProxy(w, r)
		if w.Code != tc.wantStatus {
			t.Errorf("path %q: got status %d, want %d", tc.path, w.Code, tc.wantStatus)
		}
	}
}

func TestGrafanaProxy_Unavailable(t *testing.T) {
	// Discoverer with no Grafana configured
	d := &Discoverer{
		status: &MonitoringStatus{},
	}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	r := httptest.NewRequest("GET", "/api/v1/monitoring/grafana/proxy/d/test", nil)
	w := httptest.NewRecorder()
	h.GrafanaProxy(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// --- Handler tests ---

func TestHandleStatus(t *testing.T) {
	d := &Discoverer{
		status: &MonitoringStatus{
			Prometheus: ComponentStatus{Available: true, URL: "http://prom:9090"},
			Grafana:    ComponentStatus{Available: false},
		},
	}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	r := httptest.NewRequest("GET", "/api/v1/monitoring/status", nil)
	w := httptest.NewRecorder()
	h.HandleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	var resp struct {
		Data MonitoringStatus `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Data.Prometheus.Available {
		t.Error("expected prometheus available")
	}
	if resp.Data.Grafana.Available {
		t.Error("expected grafana unavailable")
	}
}

func TestHandleQuery_NoPrometheus(t *testing.T) {
	d := &Discoverer{status: &MonitoringStatus{}}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	r := httptest.NewRequest("GET", "/api/v1/monitoring/query?query=up", nil)
	w := httptest.NewRecorder()
	h.HandleQuery(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", w.Code)
	}
}

func TestHandleQuery_MissingQuery(t *testing.T) {
	// Create a mock Prometheus server
	mockProm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer mockProm.Close()

	pc, _ := NewPrometheusClientWithTransport(mockProm.URL, http.DefaultTransport)
	d := &Discoverer{
		status:     &MonitoringStatus{Prometheus: ComponentStatus{Available: true}},
		promClient: pc,
	}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	r := httptest.NewRequest("GET", "/api/v1/monitoring/query", nil)
	w := httptest.NewRecorder()
	h.HandleQuery(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleQuery_QueryTooLong(t *testing.T) {
	pc, _ := NewPrometheusClientWithTransport("http://localhost:9090", http.DefaultTransport)
	d := &Discoverer{
		status:     &MonitoringStatus{Prometheus: ComponentStatus{Available: true}},
		promClient: pc,
	}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	longQuery := make([]byte, maxQueryLength+1)
	for i := range longQuery {
		longQuery[i] = 'a'
	}
	r := httptest.NewRequest("GET", "/api/v1/monitoring/query?query="+string(longQuery), nil)
	w := httptest.NewRecorder()
	h.HandleQuery(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleDashboards_NoGrafana(t *testing.T) {
	d := &Discoverer{status: &MonitoringStatus{}}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	r := httptest.NewRequest("GET", "/api/v1/monitoring/dashboards", nil)
	w := httptest.NewRecorder()
	h.HandleDashboards(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	var resp struct {
		Data []any `json:"data"`
	}
	json.Unmarshal(body, &resp)
	if len(resp.Data) != 0 {
		t.Errorf("expected empty dashboard list, got %d", len(resp.Data))
	}
}

func TestHandleResourceDashboard_KnownKind(t *testing.T) {
	d := &Discoverer{
		status: &MonitoringStatus{Grafana: ComponentStatus{Available: true}},
	}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	r := httptest.NewRequest("GET", "/api/v1/monitoring/resource-dashboard?kind=pods", nil)
	w := httptest.NewRecorder()
	h.HandleResourceDashboard(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data["dashboardUID"] != "kubecenter-pod-detail" {
		t.Errorf("got dashboardUID %v, want kubecenter-pod-detail", resp.Data["dashboardUID"])
	}
}

func TestHandleResourceDashboard_UnknownKind(t *testing.T) {
	d := &Discoverer{status: &MonitoringStatus{}}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	r := httptest.NewRequest("GET", "/api/v1/monitoring/resource-dashboard?kind=configmaps", nil)
	w := httptest.NewRecorder()
	h.HandleResourceDashboard(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data["available"] != false {
		t.Error("expected available=false for configmaps")
	}
}

func TestHandleTemplates(t *testing.T) {
	d := &Discoverer{status: &MonitoringStatus{}}
	h := &Handler{Discoverer: d, Logger: testLogger()}

	r := httptest.NewRequest("GET", "/api/v1/monitoring/templates", nil)
	w := httptest.NewRecorder()
	h.HandleTemplates(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	var resp struct {
		Data []QueryTemplate `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Data) == 0 {
		t.Error("expected non-empty templates list")
	}
}

// --- Dashboard embed tests ---

func TestDashboardsEmbedded(t *testing.T) {
	entries, err := dashboardsFS().ReadDir(".")
	if err != nil {
		t.Fatalf("reading embedded dashboards: %v", err)
	}

	jsonCount := 0
	for _, e := range entries {
		if e.IsDir() || e.Name() == "embed.go" {
			continue
		}
		jsonCount++
		data, err := dashboardsFS().ReadFile(e.Name())
		if err != nil {
			t.Errorf("reading %s: %v", e.Name(), err)
			continue
		}
		// Validate it's valid JSON
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Errorf("%s: invalid JSON: %v", e.Name(), err)
			continue
		}
		// Check required fields
		if _, ok := m["uid"]; !ok {
			t.Errorf("%s: missing uid field", e.Name())
		}
		if _, ok := m["title"]; !ok {
			t.Errorf("%s: missing title field", e.Name())
		}
	}
	if jsonCount < 4 {
		t.Errorf("expected at least 4 dashboard files, got %d", jsonCount)
	}
}

// --- Helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func dashboardsFS() embed.FS {
	return dashboards.FS
}
