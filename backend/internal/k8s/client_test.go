package k8s

import (
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

// TestNewFakeClientFactory verifies the test-injection seam: ClientForUser must
// return the exact injected kubernetes.Interface (bypassing impersonation), and
// baseClientset is intentionally nil. The nil-baseClientset contract is asserted
// here so a future caller that reaches for BaseClientset() / RESTMapper() on a
// fake-injected factory finds the constraint documented by a failing test rather
// than a runtime nil dereference.
func TestNewFakeClientFactory(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	f := NewFakeClientFactory(fakeCS)

	got, err := f.ClientForUser("alice", []string{"team"})
	if err != nil {
		t.Fatalf("ClientForUser returned error: %v", err)
	}
	if got != fakeCS {
		t.Errorf("ClientForUser returned %v, want the injected fake %v", got, fakeCS)
	}

	if f.baseClientset != nil {
		t.Error("NewFakeClientFactory must leave baseClientset nil (Secret handlers use only the impersonated path)")
	}
}
