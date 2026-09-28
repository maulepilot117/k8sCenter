package server

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// sarKey is one distinct access question the capabilities handler asks.
type sarKey struct{ verb, group, resource string }

// distinctSARKeys counts the distinct access questions the operation table
// asks for rows supported on the local cluster.
func distinctSARKeys() map[sarKey]bool {
	keys := map[sarKey]bool{}
	for _, op := range capabilityOperations {
		if op.supportedFor(true) {
			keys[sarKey{op.AuthVerb, op.AuthGroup, op.AuthResource}] = true
		}
	}
	return keys
}

// TestCapabilities_AccessChecksRunConcurrently pins that the handler's
// access checks overlap instead of running one after another. On a remote
// cluster every check is a SelfSubjectAccessReview round trip, so a serial
// loop costs the sum of those round trips per request. The predicate holds
// each call until a second call is in flight; a serial handler never gets
// there and every call times out instead.
func TestCapabilities_AccessChecksRunConcurrently(t *testing.T) {
	if len(distinctSARKeys()) < 2 {
		t.Skip("operation table asks fewer than two distinct access questions")
	}
	var mu sync.Mutex
	inFlight := 0
	overlapped := make(chan struct{})
	var once sync.Once
	sawOverlap := false

	ac := resources.NewPredicateAccessChecker(func(verb, apiGroup, resource, namespace string) bool {
		mu.Lock()
		inFlight++
		if inFlight >= 2 {
			once.Do(func() { close(overlapped) })
		}
		mu.Unlock()
		select {
		case <-overlapped:
			mu.Lock()
			sawOverlap = true
			mu.Unlock()
		case <-time.After(500 * time.Millisecond):
		}
		mu.Lock()
		inFlight--
		mu.Unlock()
		return true
	})
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, ac)
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawOverlap {
		t.Fatal("no two access checks were ever in flight together; the handler asks them one at a time")
	}
}

// TestCapabilities_OneAccessCheckPerDistinctQuestion pins that rows sharing
// a verb/group/resource share one access check. Several rows ask the same
// question (patch configmaps, list pods); the AccessChecker's 60s cache hides
// the repeats on a warm path, but a cold remote request paid for each one.
func TestCapabilities_OneAccessCheckPerDistinctQuestion(t *testing.T) {
	var mu sync.Mutex
	calls := map[sarKey]int{}
	ac := resources.NewPredicateAccessChecker(func(verb, apiGroup, resource, namespace string) bool {
		mu.Lock()
		calls[sarKey{verb, apiGroup, resource}]++
		mu.Unlock()
		return true
	})
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, ac)
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	want := distinctSARKeys()
	if len(calls) != len(want) {
		t.Errorf("asked %d distinct questions; want %d (%v)", len(calls), len(want), calls)
	}
	for key, n := range calls {
		if n != 1 {
			t.Errorf("%s %s/%s asked %d times; want once", key.verb, key.group, key.resource, n)
		}
	}
}

// TestCapabilities_PanickingAccessCheckDegradesOneQuestion pins the
// goroutine panic-safety convention for the fan-out: a panic inside one
// access check runs on a worker goroutine that chi's Recoverer cannot see,
// so it must be recovered there. The rows asking that question report
// authz_unknown and every other row is answered normally.
func TestCapabilities_PanickingAccessCheckDegradesOneQuestion(t *testing.T) {
	ac := resources.NewPredicateAccessChecker(func(verb, apiGroup, resource, namespace string) bool {
		if resource == "pods/exec" {
			panic("adversarial SAR response")
		}
		return true
	})
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, ac)
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)

	exec := findCapability(t, body, "pod.exec")
	if exec.Authorized != nil || exec.ReasonCode != ReasonAuthzUnknown {
		t.Errorf("pod.exec = (authorized %v, %q); want (nil, %q)", exec.Authorized, exec.ReasonCode, ReasonAuthzUnknown)
	}
	nodes := findCapability(t, body, "dashboard.summary")
	if nodes.Authorized == nil || !*nodes.Authorized || nodes.ReasonCode != ReasonOK {
		t.Errorf("dashboard.summary = (authorized %v, %q); want (true, %q)", nodes.Authorized, nodes.ReasonCode, ReasonOK)
	}
}
