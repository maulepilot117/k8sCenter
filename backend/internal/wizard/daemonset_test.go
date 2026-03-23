package wizard

import (
	"fmt"
	"strings"
	"testing"
)

func TestDaemonSetInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      DaemonSetInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid daemonset",
			input: DaemonSetInput{
				Name:      "log-collector",
				Namespace: "monitoring",
				Container: ContainerInput{Image: "fluentd:latest"},
			},
			wantErrors: 0,
		},
		{
			name: "missing name",
			input: DaemonSetInput{
				Namespace: "default",
				Container: ContainerInput{Image: "nginx"},
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name: "missing namespace",
			input: DaemonSetInput{
				Name:      "my-ds",
				Container: ContainerInput{Image: "nginx"},
			},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name: "container validation empty image",
			input: DaemonSetInput{
				Name:      "my-ds",
				Namespace: "default",
				Container: ContainerInput{Image: ""},
			},
			wantErrors: 1, wantFields: []string{"container.image"},
		},
		{
			name: "too many nodeSelector entries",
			input: func() DaemonSetInput {
				ns := make(map[string]string, 51)
				for i := 0; i < 51; i++ {
					ns[fmt.Sprintf("key-%d", i)] = "val"
				}
				return DaemonSetInput{
					Name:         "my-ds",
					Namespace:    "default",
					Container:    ContainerInput{Image: "nginx"},
					NodeSelector: ns,
				}
			}(),
			wantErrors: 1, wantFields: []string{"nodeSelector"},
		},
		{
			name: "valid with nodeSelector",
			input: DaemonSetInput{
				Name:         "my-ds",
				Namespace:    "default",
				Container:    ContainerInput{Image: "nginx"},
				NodeSelector: map[string]string{"disktype": "ssd"},
			},
			wantErrors: 0,
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

func TestDaemonSetInputToYAML(t *testing.T) {
	input := DaemonSetInput{
		Name:         "node-exporter",
		Namespace:    "monitoring",
		Container:    ContainerInput{Image: "prom/node-exporter:v1.7"},
		NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: DaemonSet") {
		t.Error("expected kind: DaemonSet")
	}
	if !strings.Contains(yaml, "name: node-exporter") {
		t.Error("expected name: node-exporter")
	}
	if !strings.Contains(yaml, "nodeSelector") {
		t.Error("expected nodeSelector in output")
	}
}
