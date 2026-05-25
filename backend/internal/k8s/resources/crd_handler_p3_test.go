package resources

import "testing"

// TestValidateCRDUpdateIdentity covers P3-4 (security audit 2026-05-22):
// CRD update requests must have body metadata.name / metadata.namespace either
// match the URL path or be empty. Explicit mismatches must be rejected so the
// audit log (anchored to the URL name) cannot be desynced from the object
// actually written.
func TestValidateCRDUpdateIdentity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		urlName   string
		urlNS     string
		scope     string
		bodyName  string
		bodyNS    string
		wantOK    bool
		wantInMsg string
	}{
		{
			name: "both empty body fields accepted (namespaced)",
			urlName: "foo", urlNS: "default", scope: "Namespaced",
			bodyName: "", bodyNS: "",
			wantOK: true,
		},
		{
			name: "matching body fields accepted (namespaced)",
			urlName: "foo", urlNS: "default", scope: "Namespaced",
			bodyName: "foo", bodyNS: "default",
			wantOK: true,
		},
		{
			name: "body name mismatch rejected",
			urlName: "foo", urlNS: "default", scope: "Namespaced",
			bodyName: "bar", bodyNS: "default",
			wantOK: false, wantInMsg: "metadata.name",
		},
		{
			name: "body namespace mismatch rejected",
			urlName: "foo", urlNS: "default", scope: "Namespaced",
			bodyName: "foo", bodyNS: "other",
			wantOK: false, wantInMsg: "metadata.namespace",
		},
		{
			name: "body name mismatch + namespace mismatch — name error wins (fail-fast)",
			urlName: "foo", urlNS: "default", scope: "Namespaced",
			bodyName: "bar", bodyNS: "other",
			wantOK: false, wantInMsg: "metadata.name",
		},
		{
			name: "cluster-scoped resource ignores body namespace",
			urlName: "foo", urlNS: "", scope: "Cluster",
			bodyName: "foo", bodyNS: "stray",
			wantOK: true,
		},
		{
			name: "cluster-scoped resource still rejects body name mismatch",
			urlName: "foo", urlNS: "", scope: "Cluster",
			bodyName: "bar", bodyNS: "",
			wantOK: false, wantInMsg: "metadata.name",
		},
		{
			name: "empty body name accepted even when body namespace mismatches — namespace check still fires",
			urlName: "foo", urlNS: "default", scope: "Namespaced",
			bodyName: "", bodyNS: "other",
			wantOK: false, wantInMsg: "metadata.namespace",
		},
		{
			name: "empty body namespace accepted on namespaced resource",
			urlName: "foo", urlNS: "default", scope: "Namespaced",
			bodyName: "foo", bodyNS: "",
			wantOK: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			msg, detail, ok := validateCRDUpdateIdentity(c.urlName, c.urlNS, c.scope, c.bodyName, c.bodyNS)
			if ok != c.wantOK {
				t.Fatalf("ok=%v want=%v (msg=%q detail=%q)", ok, c.wantOK, msg, detail)
			}
			if !c.wantOK && c.wantInMsg != "" && !contains(msg, c.wantInMsg) {
				t.Errorf("expected msg to contain %q, got %q", c.wantInMsg, msg)
			}
			if c.wantOK && (msg != "" || detail != "") {
				t.Errorf("expected empty msg+detail on accept, got msg=%q detail=%q", msg, detail)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestSplitGroupResourceKey covers P3-2 helper used to split CRDDiscovery
// count-cache keys back into (group, resource) so per-user SARs can be issued.
// Malformed keys must be rejected — never leaked into the SAR call.
func TestSplitGroupResourceKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, wantGroup, wantResource string
		wantOK                            bool
	}{
		{"normal", "cert-manager.io/certificates", "cert-manager.io", "certificates", true},
		{"single segment group", "example.com/widgets", "example.com", "widgets", true},
		{"empty key", "", "", "", false},
		{"only slash", "/", "", "", false},
		{"leading slash", "/widgets", "", "", false},
		{"trailing slash", "example.com/", "", "", false},
		{"no slash", "example.com", "", "", false},
		{"multiple slashes — split at first", "a.b/c/d", "a.b", "c/d", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			group, resource, ok := splitGroupResourceKey(c.in)
			if ok != c.wantOK {
				t.Fatalf("ok=%v want=%v (group=%q resource=%q)", ok, c.wantOK, group, resource)
			}
			if ok && (group != c.wantGroup || resource != c.wantResource) {
				t.Errorf("got group=%q resource=%q, want group=%q resource=%q",
					group, resource, c.wantGroup, c.wantResource)
			}
		})
	}
}
