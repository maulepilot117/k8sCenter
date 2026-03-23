package wizard

import (
	"strings"
	"testing"
)

func TestIngressInputValidate(t *testing.T) {
	validRule := IngressRule{
		Host: "example.com",
		Paths: []IngressPath{{Path: "/", PathType: "Prefix", ServiceName: "web", ServicePort: 80}},
	}

	tests := []struct {
		name       string
		input      IngressInput
		wantErrors int
		wantFields []string
	}{
		{
			name:       "valid",
			input:      IngressInput{Name: "my-ingress", Namespace: "default", Rules: []IngressRule{validRule}},
			wantErrors: 0,
		},
		{
			name: "valid with TLS",
			input: IngressInput{
				Name: "tls-ingress", Namespace: "default",
				Rules: []IngressRule{validRule},
				TLS:   []IngressTLS{{Hosts: []string{"example.com"}, SecretName: "tls-secret"}},
			},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      IngressInput{Namespace: "default", Rules: []IngressRule{validRule}},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      IngressInput{Name: "ing", Rules: []IngressRule{validRule}},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "no rules",
			input:      IngressInput{Name: "ing", Namespace: "default", Rules: []IngressRule{}},
			wantErrors: 1, wantFields: []string{"rules"},
		},
		{
			name: "path missing leading slash",
			input: IngressInput{Name: "ing", Namespace: "default", Rules: []IngressRule{{
				Host:  "example.com",
				Paths: []IngressPath{{Path: "noslash", PathType: "Prefix", ServiceName: "web", ServicePort: 80}},
			}}},
			wantErrors: 1, wantFields: []string{"rules[0].paths[0].path"},
		},
		{
			name: "invalid pathType",
			input: IngressInput{Name: "ing", Namespace: "default", Rules: []IngressRule{{
				Host:  "example.com",
				Paths: []IngressPath{{Path: "/", PathType: "Invalid", ServiceName: "web", ServicePort: 80}},
			}}},
			wantErrors: 1, wantFields: []string{"rules[0].paths[0].pathType"},
		},
		{
			name: "missing serviceName",
			input: IngressInput{Name: "ing", Namespace: "default", Rules: []IngressRule{{
				Paths: []IngressPath{{Path: "/", PathType: "Prefix", ServicePort: 80}},
			}}},
			wantErrors: 1, wantFields: []string{"rules[0].paths[0].serviceName"},
		},
		{
			name: "invalid port",
			input: IngressInput{Name: "ing", Namespace: "default", Rules: []IngressRule{{
				Paths: []IngressPath{{Path: "/", PathType: "Prefix", ServiceName: "web", ServicePort: 0}},
			}}},
			wantErrors: 1, wantFields: []string{"rules[0].paths[0].servicePort"},
		},
		{
			name: "no paths in rule",
			input: IngressInput{Name: "ing", Namespace: "default", Rules: []IngressRule{{
				Host: "example.com", Paths: []IngressPath{},
			}}},
			wantErrors: 1, wantFields: []string{"rules[0].paths"},
		},
		{
			name: "TLS missing hosts",
			input: IngressInput{
				Name: "ing", Namespace: "default", Rules: []IngressRule{validRule},
				TLS: []IngressTLS{{SecretName: "secret"}},
			},
			wantErrors: 1, wantFields: []string{"tls[0].hosts"},
		},
		{
			name: "TLS missing secretName",
			input: IngressInput{
				Name: "ing", Namespace: "default", Rules: []IngressRule{validRule},
				TLS: []IngressTLS{{Hosts: []string{"example.com"}}},
			},
			wantErrors: 1, wantFields: []string{"tls[0].secretName"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if len(errs) != tt.wantErrors {
				t.Errorf("expected %d errors, got %d: %v", tt.wantErrors, len(errs), errs)
			}
			for _, wf := range tt.wantFields {
				found := false
				for _, e := range errs {
					if e.Field == wf {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, not found in %v", wf, errs)
				}
			}
		})
	}
}

func TestIngressInputToYAML(t *testing.T) {
	className := "nginx"
	input := IngressInput{
		Name: "web-ingress", Namespace: "prod",
		IngressClassName: &className,
		Rules: []IngressRule{{
			Host: "app.example.com",
			Paths: []IngressPath{{
				Path: "/api", PathType: "Prefix", ServiceName: "backend", ServicePort: 8080,
			}},
		}},
		TLS: []IngressTLS{{Hosts: []string{"app.example.com"}, SecretName: "tls-cert"}},
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: Ingress") {
		t.Error("expected kind: Ingress")
	}
	if !strings.Contains(yaml, "networking.k8s.io/v1") {
		t.Error("expected apiVersion")
	}
	if !strings.Contains(yaml, "app.example.com") {
		t.Error("expected host")
	}
	if !strings.Contains(yaml, "ingressClassName: nginx") {
		t.Error("expected ingressClassName")
	}
	if !strings.Contains(yaml, "tls-cert") {
		t.Error("expected TLS secret name")
	}
}
