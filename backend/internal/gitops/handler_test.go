package gitops

import "testing"

func TestParseCompositeID(t *testing.T) {
	tests := []struct {
		input     string
		wantTool  string
		wantNS    string
		wantName  string
		wantError bool
	}{
		{"argo:argocd:my-app", "argo", "argocd", "my-app", false},
		{"flux-ks:flux-system:my-ks", "flux-ks", "flux-system", "my-ks", false},
		{"flux-hr:default:my-release", "flux-hr", "default", "my-release", false},
		{"argo:ns:name-with-colons:extra", "argo", "ns", "name-with-colons:extra", false}, // SplitN(3) keeps extra in name
		{"", "", "", "", true},
		{"onlyonepart", "", "", "", true},
		{"two:parts", "", "", "", true},
		{":empty:prefix", "", "", "", true},
		{"empty::name", "", "", "", true},
		{"empty:ns:", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tool, ns, name, err := parseCompositeID(tt.input)
			if tt.wantError {
				if err == nil {
					t.Errorf("expected error for input %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if tool != tt.wantTool {
				t.Errorf("tool: got %q, want %q", tool, tt.wantTool)
			}
			if ns != tt.wantNS {
				t.Errorf("namespace: got %q, want %q", ns, tt.wantNS)
			}
			if name != tt.wantName {
				t.Errorf("name: got %q, want %q", name, tt.wantName)
			}
		})
	}
}

func TestToolGVR(t *testing.T) {
	tests := []struct {
		prefix    string
		wantOK    bool
		wantGroup string
	}{
		{"argo", true, "argoproj.io"},
		{"flux-ks", true, "kustomize.toolkit.fluxcd.io"},
		{"flux-hr", true, "helm.toolkit.fluxcd.io"},
		{"unknown", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			group, _, ok := toolGVR(tt.prefix)
			if ok != tt.wantOK {
				t.Errorf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if group != tt.wantGroup {
				t.Errorf("group: got %q, want %q", group, tt.wantGroup)
			}
		})
	}
}
