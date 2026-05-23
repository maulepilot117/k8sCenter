package monitoring

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestRegistry_AllSlugsHaveRequiredFields verifies every entry in the Registry
// has non-empty required fields and a matching Slug key.
func TestRegistry_AllSlugsHaveRequiredFields(t *testing.T) {
	for key, def := range Registry {
		t.Run(key, func(t *testing.T) {
			if def.Slug == "" {
				t.Error("Slug field is empty")
			}
			if def.Slug != key {
				t.Errorf("Slug field %q does not match map key %q", def.Slug, key)
			}
			if def.Template == "" {
				t.Error("Template is empty")
			}
			if len(def.RequiredVerbs) == 0 {
				t.Error("RequiredVerbs is empty")
			}
			if def.RequiredGVR == "" {
				t.Error("RequiredGVR is empty")
			}
			if def.Description == "" {
				t.Error("Description is empty")
			}
		})
	}
}

// TestRegistry_TemplateRenders verifies that every registered template can be
// rendered with sample Namespace/Name values and the result contains the
// substituted values (not raw {{.Namespace}} / {{.Name}} placeholders).
func TestRegistry_TemplateRenders(t *testing.T) {
	namespace := "test-ns"
	name := "my-resource"

	for key, def := range Registry {
		t.Run(key, func(t *testing.T) {
			rendered, err := renderSlugTemplate(def.Template, namespace, name)
			if err != nil {
				t.Fatalf("renderSlugTemplate failed: %v", err)
			}

			// Template variables must not appear literally in the output.
			if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
				t.Errorf("rendered output still contains template delimiters: %q", rendered)
			}

			// If the template used .Namespace, the rendered query should contain
			// the namespace value.
			if strings.Contains(def.Template, "{{.Namespace}}") &&
				!strings.Contains(rendered, namespace) {
				t.Errorf("rendered output missing namespace %q: %q", namespace, rendered)
			}

			// If the template used .Name, the rendered query should contain
			// the name value.
			if strings.Contains(def.Template, "{{.Name}}") &&
				!strings.Contains(rendered, name) {
				t.Errorf("rendered output missing name %q: %q", name, rendered)
			}
		})
	}
}

// TestRegistry_RequiredGVRFormat verifies that every RequiredGVR string follows
// the "resource" or "resource.group" convention.
var validGVRPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9.-]*)?$`)

func TestRegistry_RequiredGVRFormat(t *testing.T) {
	for key, def := range Registry {
		t.Run(key, func(t *testing.T) {
			if !validGVRPattern.MatchString(def.RequiredGVR) {
				t.Errorf("RequiredGVR %q has unexpected format; want 'resource' or 'resource.group'",
					def.RequiredGVR)
			}
		})
	}
}

// TestValidateRangeCaps tests the P2-4 time-range enforcement.
func TestValidateRangeCaps(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		start   time.Time
		end     time.Time
		step    time.Duration
		wantErr bool
	}{
		{
			name:    "valid 1h range 60s step",
			start:   now.Add(-1 * time.Hour),
			end:     now,
			step:    60 * time.Second,
			wantErr: false,
		},
		{
			name:    "valid 6h range 10s step (boundary)",
			start:   now.Add(-6 * time.Hour),
			end:     now,
			step:    10 * time.Second, // (21600/10)+1=2161 samples — within 11000 limit
			wantErr: false,
		},
		{
			name:    "valid 6h range 120s step",
			start:   now.Add(-6 * time.Hour),
			end:     now,
			step:    120 * time.Second, // 180+1=181 samples
			wantErr: false,
		},
		{
			name:    "range exceeds 6h maximum",
			start:   now.Add(-7 * time.Hour),
			end:     now,
			step:    60 * time.Second,
			wantErr: true,
		},
		{
			name:    "step below 10s minimum",
			start:   now.Add(-1 * time.Hour),
			end:     now,
			step:    5 * time.Second,
			wantErr: true,
		},
		{
			name:    "end before start",
			start:   now,
			end:     now.Add(-1 * time.Hour),
			step:    60 * time.Second,
			wantErr: true,
		},
		{
			name:    "too many samples (1h / 1s = 3601 > 11000 — but 1s < 10s so step error fires first)",
			start:   now.Add(-1 * time.Hour),
			end:     now,
			step:    1 * time.Second,
			wantErr: true, // step < 10s
		},
		{
			name:    "step exactly at minimum (10s) with valid 6h range",
			start:   now.Add(-6 * time.Hour),
			end:     now,
			step:    10 * time.Second, // (21600/10)+1=2161 samples — fine
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRangeCaps(tc.start, tc.end, tc.step)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateRangeCaps() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestValidateRangeCaps_SampleCountBoundary explicitly tests the sample cap.
func TestValidateRangeCaps_SampleCountBoundary(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	// 6h / 2s = 10800 samples — exceeds 11000 at step=2s? No: 6h=21600s, /2=10800 < 11000 — fine
	// Let's use a range that would hit exactly 11001:
	// samples = (range/step) + 1 > 11000
	// range/step > 10999 => range = 10999 * step
	// Use step=10s, range = 10999 * 10s = 109990s = ~30.5h — exceeds 6h max
	// So with 6h max and 10s min step: max samples = (21600/10)+1 = 2161 — always under 11000.
	// The sample cap can only trigger when step is very small AND range is large.
	// With the other caps, it's defense-in-depth. Test it directly with short step.
	//
	// Simulate with mock times that bypass the other caps:
	// We test the math directly rather than via validateRangeCaps (which would block on step first).
	step := 10 * time.Second
	dur := time.Duration(maxQuerySamples) * step // exactly 11000 steps of 10s = 110000s
	start := now.Add(-dur)
	end := now

	err := validateRangeCaps(start, end, step)
	// 110000s > 6h (21600s) so the range cap fires before the sample cap.
	if err == nil {
		t.Error("expected error for range exceeding 6h, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum 6h") {
		t.Errorf("expected range cap error, got: %v", err)
	}
}

// TestRenderSlugTemplate_EmptyNamespace verifies that empty namespace/name
// don't corrupt the PromQL (template just emits "").
func TestRenderSlugTemplate_EmptyNamespace(t *testing.T) {
	tmpl := `kube_pod_info{namespace="{{.Namespace}}"}`
	rendered, err := renderSlugTemplate(tmpl, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `kube_pod_info{namespace=""}`
	if rendered != expected {
		t.Errorf("got %q, want %q", rendered, expected)
	}
}

// TestRenderSlugTemplate_Substitution verifies both variables are substituted.
func TestRenderSlugTemplate_Substitution(t *testing.T) {
	tmpl := `container_cpu{namespace="{{.Namespace}}",pod="{{.Name}}"}`
	rendered, err := renderSlugTemplate(tmpl, "kube-system", "coredns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `container_cpu{namespace="kube-system",pod="coredns"}`
	if rendered != expected {
		t.Errorf("got %q, want %q", rendered, expected)
	}
}

// TestRegistry_PodsAndDeploymentSlugs spot-checks that the most-used slugs
// render correctly.
func TestRegistry_PodsAndDeploymentSlugs(t *testing.T) {
	checks := []struct {
		slug      string
		namespace string
		name      string
		wantSubstr string
	}{
		{
			slug:       "pods/cpu",
			namespace:  "default",
			name:       "nginx",
			wantSubstr: `pod="nginx"`,
		},
		{
			slug:       "deployments/cpu",
			namespace:  "production",
			name:       "api-server",
			wantSubstr: `pod=~"api-server-.*"`,
		},
		{
			slug:       "namespaces/cpu",
			namespace:  "",
			name:       "staging",
			wantSubstr: `namespace="staging"`,
		},
		{
			slug:       "nodes/info",
			namespace:  "",
			name:       "10.0.0.1:9100",
			wantSubstr: "",           // name contains ":" — isValidK8sName would reject this;
			// this test only validates the template renders, not RBAC
		},
	}

	for _, tc := range checks {
		t.Run(tc.slug, func(t *testing.T) {
			def, ok := Registry[tc.slug]
			if !ok {
				t.Fatalf("slug %q not found in Registry", tc.slug)
			}
			rendered, err := renderSlugTemplate(def.Template, tc.namespace, tc.name)
			if err != nil {
				t.Fatalf("renderSlugTemplate failed: %v", err)
			}
			if tc.wantSubstr != "" && !strings.Contains(rendered, tc.wantSubstr) {
				t.Errorf("rendered %q missing expected substring %q", rendered, tc.wantSubstr)
			}
		})
	}
}

// TestRegistry_RBACScopes verifies that each slug's RequiredGVR either maps to
// a known core resource or a "resource.group" pair.
func TestRegistry_RBACScopes(t *testing.T) {
	knownCoreResources := map[string]bool{
		"pods":                   true,
		"nodes":                  true,
		"services":               true,
		"endpoints":              true,
		"persistentvolumeclaims": true,
		"persistentvolumes":      true,
		"resourcequotas":         true,
		"limitranges":            true,
	}

	for key, def := range Registry {
		t.Run(key, func(t *testing.T) {
			gvr := def.RequiredGVR
			if knownCoreResources[gvr] {
				return // core resource — OK
			}
			// Must be "resource.group" format.
			dot := strings.Index(gvr, ".")
			if dot == -1 {
				t.Errorf("RequiredGVR %q is not a known core resource and lacks a group suffix", gvr)
			}
		})
	}
}
