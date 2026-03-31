package resources

import (
	"testing"
)

func TestDNSSubdomainRegexp(t *testing.T) {
	valid := []string{
		"cert-manager.io",
		"certificates",
		"a",
		"a1",
		"my-group.example.com",
		"x.y.z",
		"abc123",
		"a-b",
		"0abc",
	}
	for _, s := range valid {
		if !dnsSubdomainRegexp.MatchString(s) {
			t.Errorf("expected %q to be valid, but it was rejected", s)
		}
	}

	invalid := []string{
		"",
		"../etc/passwd",
		"UPPERCASE",
		"has space",
		"-starts-with-dash",
		"ends-with-dash-",
		".starts-with-dot",
		"ends-with-dot.",
		"has/slash",
		"has@symbol",
		"CamelCase",
	}
	for _, s := range invalid {
		if dnsSubdomainRegexp.MatchString(s) {
			t.Errorf("expected %q to be invalid, but it was accepted", s)
		}
	}
}

func TestResolveGVR_RejectsInvalidNames(t *testing.T) {
	// These tests verify that resolveGVR rejects path-traversal and other
	// invalid group/resource names via the dnsSubdomainRegexp. We test the
	// regexp directly since resolveGVR requires a full HTTP handler setup.
	attacks := []string{
		"../etc/passwd",
		"..%2fetc%2fpasswd",
		"",
		"UPPERCASE",
		"-leading-dash",
		"trailing-dash-",
		".leading-dot",
		"trailing-dot.",
		"with/slash",
		"with spaces",
	}

	for _, input := range attacks {
		if dnsSubdomainRegexp.MatchString(input) {
			t.Errorf("resolveGVR should reject %q but dnsSubdomainRegexp matched", input)
		}
	}
}
