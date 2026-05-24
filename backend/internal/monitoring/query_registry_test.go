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

			// If the template used .Namespace (or .NamespaceRegex), the rendered
			// query should contain the namespace value (regex-escaped for the
			// =~ form, but the sample value has no metachars).
			if (strings.Contains(def.Template, "{{.Namespace}}") ||
				strings.Contains(def.Template, "{{.NamespaceRegex}}")) &&
				!strings.Contains(rendered, namespace) {
				t.Errorf("rendered output missing namespace %q: %q", namespace, rendered)
			}

			// If the template used .Name (or .NameRegex), the rendered query
			// should contain the name value.
			if (strings.Contains(def.Template, "{{.Name}}") ||
				strings.Contains(def.Template, "{{.NameRegex}}")) &&
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

// TestValidateRangeCaps_RangeCapBeatsSampleMath confirms that the active
// range cap (6h) and step cap (10s) bound the sample count implicitly. The
// previous explicit maxQuerySamples gate was removed (F#28) — it was dead code
// behind the other two caps. This test pins that property so a future change
// loosening the range or step cap is forced to revisit sample bounding.
func TestValidateRangeCaps_RangeCapBeatsSampleMath(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	// At the legal extremes (range=6h, step=10s) we get (21600/10)+1 = 2161
	// samples — well under any reasonable Prometheus bound.
	step := minQueryStep
	start := now.Add(-maxQueryRangeDuration)
	end := now

	if err := validateRangeCaps(start, end, step); err != nil {
		t.Errorf("legal extreme range should pass, got: %v", err)
	}

	// One second past the range cap → range cap fires (not a sample cap).
	start = now.Add(-(maxQueryRangeDuration + time.Second))
	err := validateRangeCaps(start, end, step)
	if err == nil {
		t.Fatal("expected error for range exceeding 6h, got nil")
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

// TestRenderSlugTemplate_RegexEscape verifies that .NameRegex / .NamespaceRegex
// escape PromQL regex metacharacters so a validated value like "1.2.3.4:9100"
// doesn't widen the surrounding =~ pattern. F#30.
func TestRenderSlugTemplate_RegexEscape(t *testing.T) {
	tmpl := `node_load5{instance=~"{{.NameRegex}}"}`
	rendered, err := renderSlugTemplate(tmpl, "", "1.2.3.4:9100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `node_load5{instance=~"1\.2\.3\.4:9100"}`
	if rendered != expected {
		t.Errorf("got %q, want %q", rendered, expected)
	}
}

// TestEscapePromQLRegex_MetacharCoverage checks the escaper handles the
// full RE2 metaset, not just `.`. Future validator changes might let new
// metachars through; this test pins the defense.
func TestEscapePromQLRegex_MetacharCoverage(t *testing.T) {
	cases := map[string]string{
		"foo.bar":   `foo\.bar`,
		"foo+bar":   `foo\+bar`,
		"foo*bar":   `foo\*bar`,
		"foo?bar":   `foo\?bar`,
		"foo(bar)":  `foo\(bar\)`,
		"foo[bar]":  `foo\[bar\]`,
		"foo{bar}":  `foo\{bar\}`,
		"^foo$":     `\^foo\$`,
		"foo|bar":   `foo\|bar`,
		`back\slash`: `back\\slash`,
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got := escapePromQLRegex(input)
			if got != want {
				t.Errorf("escapePromQLRegex(%q) = %q, want %q", input, got, want)
			}
		})
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
