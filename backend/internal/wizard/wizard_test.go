package wizard

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
)

// --- DeploymentInput validation tests ---

func TestDeploymentValidate_Valid(t *testing.T) {
	d := DeploymentInput{
		Name:      "my-app",
		Namespace: "default",
		Image:     "nginx:1.25",
		Replicas:  3,
	}
	if errs := d.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestDeploymentValidate_InvalidName(t *testing.T) {
	tests := []struct {
		name string
	}{
		{""},
		{"MyApp"},                 // uppercase
		{"-start-dash"},           // starts with dash
		{"end-dash-"},             // ends with dash
		{"has spaces"},            // spaces
		{strings.Repeat("a", 64)}, // too long
	}
	for _, tt := range tests {
		d := DeploymentInput{Name: tt.name, Namespace: "default", Image: "nginx"}
		errs := d.Validate()
		found := false
		for _, e := range errs {
			if e.Field == "name" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected name validation error for %q, got none", tt.name)
		}
	}
}

func TestDeploymentValidate_MissingRequired(t *testing.T) {
	d := DeploymentInput{Name: "ok", Namespace: "", Image: ""}
	errs := d.Validate()
	fields := map[string]bool{}
	for _, e := range errs {
		fields[e.Field] = true
	}
	if !fields["namespace"] {
		t.Error("expected namespace error")
	}
	if !fields["image"] {
		t.Error("expected image error")
	}
}

func TestDeploymentValidate_ReplicasRange(t *testing.T) {
	for _, r := range []int32{-1, 1001} {
		d := DeploymentInput{Name: "ok", Namespace: "default", Image: "nginx", Replicas: r}
		errs := d.Validate()
		found := false
		for _, e := range errs {
			if e.Field == "replicas" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected replicas error for %d", r)
		}
	}
}

func TestDeploymentValidate_Ports(t *testing.T) {
	d := DeploymentInput{
		Name: "ok", Namespace: "default", Image: "nginx",
		Ports: []PortInput{
			{ContainerPort: 0}, // invalid
			{ContainerPort: 80},
			{ContainerPort: 80},                    // duplicate
			{ContainerPort: 443, Protocol: "SCTP"}, // bad protocol
		},
	}
	errs := d.Validate()
	if len(errs) < 3 {
		t.Errorf("expected at least 3 port errors, got %d: %v", len(errs), errs)
	}
}

func TestDeploymentValidate_EnvVars(t *testing.T) {
	d := DeploymentInput{
		Name: "ok", Namespace: "default", Image: "nginx",
		EnvVars: []EnvVarInput{
			{Name: "123BAD"},                                         // invalid name
			{Name: "GOOD_NAME"},                                      // no value or ref
			{Name: "REF_NO_KEY", ConfigMapRef: "my-cm"},              // ref without key
			{Name: "OK", Value: "hello"},                             // valid
			{Name: "OK_REF", ConfigMapRef: "my-cm", Key: "data-key"}, // valid
		},
	}
	errs := d.Validate()
	// 123BAD → invalid name + no value (2), GOOD_NAME → no value (1), REF_NO_KEY → missing key (1) = 4
	if len(errs) != 4 {
		t.Errorf("expected 4 env var errors, got %d: %v", len(errs), errs)
	}
}

func TestDeploymentValidate_Resources(t *testing.T) {
	d := DeploymentInput{
		Name: "ok", Namespace: "default", Image: "nginx",
		Resources: &ResourcesInput{
			RequestCPU:    "not-a-quantity",
			RequestMemory: "128Mi", // valid
		},
	}
	errs := d.Validate()
	found := false
	for _, e := range errs {
		if e.Field == "resources.requestCpu" {
			found = true
		}
	}
	if !found {
		t.Error("expected resource quantity validation error")
	}
}

func TestDeploymentValidate_Probes(t *testing.T) {
	d := DeploymentInput{
		Name: "ok", Namespace: "default", Image: "nginx",
		Probes: &ProbesInput{
			Liveness:  &ProbeInput{Type: "http", Port: 0}, // missing path, bad port
			Readiness: &ProbeInput{Type: "badtype", Port: 80},
		},
	}
	errs := d.Validate()
	if len(errs) < 3 {
		t.Errorf("expected at least 3 probe errors, got %d: %v", len(errs), errs)
	}
}

func TestDeploymentValidate_Strategy(t *testing.T) {
	d := DeploymentInput{
		Name: "ok", Namespace: "default", Image: "nginx",
		Strategy: &StrategyInput{Type: "BadStrategy"},
	}
	errs := d.Validate()
	found := false
	for _, e := range errs {
		if e.Field == "strategy.type" {
			found = true
		}
	}
	if !found {
		t.Error("expected strategy type error")
	}
}

// --- DeploymentInput.ToDeployment tests ---

func TestToDeployment_Basic(t *testing.T) {
	d := DeploymentInput{
		Name: "my-app", Namespace: "test", Image: "nginx:latest", Replicas: 2,
	}
	dep, err := d.ToDeployment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dep.TypeMeta.APIVersion != "apps/v1" {
		t.Errorf("expected apiVersion apps/v1, got %s", dep.TypeMeta.APIVersion)
	}
	if dep.TypeMeta.Kind != "Deployment" {
		t.Errorf("expected kind Deployment, got %s", dep.TypeMeta.Kind)
	}
	if dep.Name != "my-app" || dep.Namespace != "test" {
		t.Errorf("unexpected name/namespace: %s/%s", dep.Name, dep.Namespace)
	}
	if *dep.Spec.Replicas != 2 {
		t.Errorf("expected 2 replicas, got %d", *dep.Spec.Replicas)
	}
	if dep.Labels["app"] != "my-app" {
		t.Error("expected auto-generated app label")
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "nginx:latest" {
		t.Error("unexpected container image")
	}
}

func TestToDeployment_WithPorts(t *testing.T) {
	d := DeploymentInput{
		Name: "app", Namespace: "default", Image: "img",
		Ports: []PortInput{{Name: "http", ContainerPort: 80, Protocol: "TCP"}, {ContainerPort: 443, Protocol: "UDP"}},
	}
	dep, err := d.ToDeployment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ports := dep.Spec.Template.Spec.Containers[0].Ports
	if len(ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(ports))
	}
	if ports[0].Name != "http" || ports[0].ContainerPort != 80 {
		t.Errorf("unexpected port 0: %+v", ports[0])
	}
}

func TestToDeployment_WithEnvVars(t *testing.T) {
	d := DeploymentInput{
		Name: "app", Namespace: "default", Image: "img",
		EnvVars: []EnvVarInput{
			{Name: "SIMPLE", Value: "hello"},
			{Name: "FROM_CM", ConfigMapRef: "my-cm", Key: "k1"},
			{Name: "FROM_SEC", SecretRef: "my-sec", Key: "k2"},
		},
	}
	dep, err := d.ToDeployment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	envs := dep.Spec.Template.Spec.Containers[0].Env
	if len(envs) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(envs))
	}
	if envs[0].Value != "hello" {
		t.Error("expected literal value")
	}
	if envs[1].ValueFrom == nil || envs[1].ValueFrom.ConfigMapKeyRef == nil {
		t.Error("expected configmap ref")
	}
	if envs[2].ValueFrom == nil || envs[2].ValueFrom.SecretKeyRef == nil {
		t.Error("expected secret ref")
	}
}

func TestToDeployment_WithResources(t *testing.T) {
	d := DeploymentInput{
		Name: "app", Namespace: "default", Image: "img",
		Resources: &ResourcesInput{RequestCPU: "100m", LimitMemory: "512Mi"},
	}
	dep, err := d.ToDeployment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := dep.Spec.Template.Spec.Containers[0].Resources
	if res.Requests.Cpu().String() != "100m" {
		t.Errorf("unexpected CPU request: %s", res.Requests.Cpu().String())
	}
	if res.Limits.Memory().String() != "512Mi" {
		t.Errorf("unexpected memory limit: %s", res.Limits.Memory().String())
	}
}

func TestToDeployment_WithProbes(t *testing.T) {
	d := DeploymentInput{
		Name: "app", Namespace: "default", Image: "img",
		Probes: &ProbesInput{
			Liveness:  &ProbeInput{Type: "http", Path: "/healthz", Port: 8080, PeriodSeconds: 10},
			Readiness: &ProbeInput{Type: "tcp", Port: 3306},
		},
	}
	dep, err := d.ToDeployment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lp := dep.Spec.Template.Spec.Containers[0].LivenessProbe
	rp := dep.Spec.Template.Spec.Containers[0].ReadinessProbe
	if lp == nil || lp.HTTPGet == nil || lp.HTTPGet.Path != "/healthz" {
		t.Error("expected HTTP liveness probe")
	}
	if rp == nil || rp.TCPSocket == nil {
		t.Error("expected TCP readiness probe")
	}
}

func TestToDeployment_WithStrategy(t *testing.T) {
	d := DeploymentInput{
		Name: "app", Namespace: "default", Image: "img",
		Strategy: &StrategyInput{Type: "RollingUpdate", MaxSurge: "25%", MaxUnavailable: "1"},
	}
	dep, err := d.ToDeployment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(dep.Spec.Strategy.Type) != "RollingUpdate" {
		t.Error("expected RollingUpdate strategy")
	}
	if dep.Spec.Strategy.RollingUpdate == nil {
		t.Fatal("expected RollingUpdate params")
	}
	if dep.Spec.Strategy.RollingUpdate.MaxSurge.String() != "25%" {
		t.Errorf("unexpected maxSurge: %s", dep.Spec.Strategy.RollingUpdate.MaxSurge.String())
	}
}

func TestToDeployment_PreservesExistingLabels(t *testing.T) {
	d := DeploymentInput{
		Name: "app", Namespace: "default", Image: "img",
		Labels: map[string]string{"team": "backend", "app": "custom"},
	}
	dep, err := d.ToDeployment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dep.Labels["app"] != "custom" {
		t.Error("should not override existing app label")
	}
	if dep.Labels["team"] != "backend" {
		t.Error("should preserve custom labels")
	}
}

// --- ServiceInput validation tests ---

func TestServiceValidate_Valid(t *testing.T) {
	s := ServiceInput{
		Name: "my-svc", Namespace: "default", Type: "ClusterIP",
		Selector: map[string]string{"app": "my-app"},
		Ports:    []ServicePortInput{{Port: 80, TargetPort: 8080}},
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestServiceValidate_InvalidType(t *testing.T) {
	s := ServiceInput{
		Name: "svc", Namespace: "default", Type: "ExternalName",
		Selector: map[string]string{"app": "x"},
		Ports:    []ServicePortInput{{Port: 80, TargetPort: 80}},
	}
	errs := s.Validate()
	found := false
	for _, e := range errs {
		if e.Field == "type" {
			found = true
		}
	}
	if !found {
		t.Error("expected type validation error")
	}
}

func TestServiceValidate_EmptySelector(t *testing.T) {
	s := ServiceInput{
		Name: "svc", Namespace: "default", Type: "ClusterIP",
		Ports: []ServicePortInput{{Port: 80, TargetPort: 80}},
	}
	errs := s.Validate()
	found := false
	for _, e := range errs {
		if e.Field == "selector" {
			found = true
		}
	}
	if !found {
		t.Error("expected selector validation error")
	}
}

func TestServiceValidate_NoPorts(t *testing.T) {
	s := ServiceInput{
		Name: "svc", Namespace: "default", Type: "ClusterIP",
		Selector: map[string]string{"app": "x"},
	}
	errs := s.Validate()
	found := false
	for _, e := range errs {
		if e.Field == "ports" {
			found = true
		}
	}
	if !found {
		t.Error("expected ports validation error")
	}
}

func TestServiceValidate_NodePortRestrictions(t *testing.T) {
	// NodePort on ClusterIP service should fail
	s := ServiceInput{
		Name: "svc", Namespace: "default", Type: "ClusterIP",
		Selector: map[string]string{"app": "x"},
		Ports:    []ServicePortInput{{Port: 80, TargetPort: 80, NodePort: 30080}},
	}
	errs := s.Validate()
	if len(errs) == 0 {
		t.Error("expected nodePort error for ClusterIP service")
	}

	// NodePort out of range on NodePort service
	s.Type = "NodePort"
	s.Ports[0].NodePort = 29000
	errs = s.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "30000") {
			found = true
		}
	}
	if !found {
		t.Error("expected nodePort range error")
	}
}

// --- ServiceInput.ToService tests ---

func TestToService_Basic(t *testing.T) {
	s := ServiceInput{
		Name: "my-svc", Namespace: "prod", Type: "ClusterIP",
		Selector: map[string]string{"app": "my-app"},
		Ports:    []ServicePortInput{{Port: 80, TargetPort: 8080}},
	}
	svc := s.ToService()

	if svc.TypeMeta.APIVersion != "v1" {
		t.Errorf("expected apiVersion v1, got %s", svc.TypeMeta.APIVersion)
	}
	if svc.TypeMeta.Kind != "Service" {
		t.Errorf("expected kind Service, got %s", svc.TypeMeta.Kind)
	}
	if svc.Name != "my-svc" || svc.Namespace != "prod" {
		t.Error("unexpected name/namespace")
	}
	if string(svc.Spec.Type) != "ClusterIP" {
		t.Errorf("unexpected type: %s", svc.Spec.Type)
	}
	if svc.Labels["app"] != "my-svc" {
		t.Error("expected auto-generated app label")
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatal("expected 1 port")
	}
	if svc.Spec.Ports[0].TargetPort.IntValue() != 8080 {
		t.Errorf("expected targetPort 8080, got %d", svc.Spec.Ports[0].TargetPort.IntValue())
	}
}

func TestToService_NodePort(t *testing.T) {
	s := ServiceInput{
		Name: "svc", Namespace: "default", Type: "NodePort",
		Selector: map[string]string{"app": "x"},
		Ports:    []ServicePortInput{{Port: 80, TargetPort: 80, NodePort: 30080, Protocol: "UDP"}},
	}
	svc := s.ToService()
	if svc.Spec.Ports[0].NodePort != 30080 {
		t.Errorf("expected nodePort 30080, got %d", svc.Spec.Ports[0].NodePort)
	}
	if svc.Spec.Ports[0].Protocol != "UDP" {
		t.Errorf("expected UDP protocol, got %s", svc.Spec.Ports[0].Protocol)
	}
}

// --- Handler tests ---

func testHandler() *Handler {
	return &Handler{Logger: slog.Default()}
}

func addAuthContext(r *http.Request) *http.Request {
	ctx := auth.ContextWithUser(r.Context(), &auth.User{
		Username:           "admin",
		KubernetesUsername: "admin",
	})
	return r.WithContext(ctx)
}

func TestHandleDeploymentPreview_Valid(t *testing.T) {
	h := testHandler()
	input := DeploymentInput{
		Name: "test-dep", Namespace: "default", Image: "nginx:latest", Replicas: 1,
		Ports: []PortInput{{ContainerPort: 80}},
	}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/deployment/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = addAuthContext(req)

	rr := httptest.NewRecorder()
	h.HandlePreview(func() WizardInput { return &DeploymentInput{} })(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data in response")
	}
	yaml, ok := data["yaml"].(string)
	if !ok || yaml == "" {
		t.Fatal("expected non-empty yaml in response")
	}
	if !strings.Contains(yaml, "kind: Deployment") {
		t.Error("expected YAML to contain 'kind: Deployment'")
	}
	if !strings.Contains(yaml, "apiVersion: apps/v1") {
		t.Error("expected YAML to contain 'apiVersion: apps/v1'")
	}
}

func TestHandleDeploymentPreview_ValidationError(t *testing.T) {
	h := testHandler()
	input := DeploymentInput{Name: "INVALID", Namespace: "", Image: ""}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/deployment/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = addAuthContext(req)

	rr := httptest.NewRecorder()
	h.HandlePreview(func() WizardInput { return &DeploymentInput{} })(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleDeploymentPreview_BadJSON(t *testing.T) {
	h := testHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/deployment/preview", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	req = addAuthContext(req)

	rr := httptest.NewRecorder()
	h.HandlePreview(func() WizardInput { return &DeploymentInput{} })(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleDeploymentPreview_NoAuth(t *testing.T) {
	h := testHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/deployment/preview", nil)
	rr := httptest.NewRecorder()
	h.HandlePreview(func() WizardInput { return &DeploymentInput{} })(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestHandleServicePreview_Valid(t *testing.T) {
	h := testHandler()
	input := ServiceInput{
		Name: "test-svc", Namespace: "default", Type: "ClusterIP",
		Selector: map[string]string{"app": "my-app"},
		Ports:    []ServicePortInput{{Port: 80, TargetPort: 8080}},
	}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/service/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = addAuthContext(req)

	rr := httptest.NewRecorder()
	h.HandlePreview(func() WizardInput { return &ServiceInput{} })(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	data, _ := resp["data"].(map[string]any)
	yaml, _ := data["yaml"].(string)
	if !strings.Contains(yaml, "kind: Service") {
		t.Error("expected YAML to contain 'kind: Service'")
	}
}

func TestHandleServicePreview_ValidationError(t *testing.T) {
	h := testHandler()
	input := ServiceInput{Name: "INVALID", Type: "BadType"}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/service/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = addAuthContext(req)

	rr := httptest.NewRecorder()
	h.HandlePreview(func() WizardInput { return &ServiceInput{} })(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rr.Code)
	}
}
