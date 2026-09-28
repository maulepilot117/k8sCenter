package k8s

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
)

// TestNewRemoteRESTConfig_RefusesPrivateAddressAtDial pins R-4: every
// remote-cluster client must re-check the resolved address on each dial, not
// only when ValidateRemoteURL ran. The fake API server listens on 127.0.0.1,
// standing in for a host that passed validation and was then rebound to a
// blocked address. The control request proves the server is reachable, so
// the refusal comes from the dialer and not from a dead endpoint.
func TestNewRemoteRESTConfig_RefusesPrivateAddressAtDial(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":"1","minor":"35","gitVersion":"v1.35.0"}`))
	}))
	defer srv.Close()

	serverVersion := func(t *testing.T, strict bool) error {
		t.Helper()
		cfg := NewRemoteRESTConfig(srv.URL, "token", nil)
		if err := ApplyClusterTLS(cfg, "test", nil, true, nil); err != nil {
			t.Fatalf("ApplyClusterTLS: %v", err)
		}
		if !strict {
			cfg.Dial = nil
		}
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			t.Fatalf("NewForConfig: %v", err)
		}
		_, err = cs.Discovery().ServerVersion()
		return err
	}

	if err := serverVersion(t, false); err != nil {
		t.Fatalf("control request without the strict dialer failed, so the test cannot tell a refusal from a dead server: %v", err)
	}

	err := serverVersion(t, true)
	if err == nil {
		t.Fatal("expected the strict dialer to refuse a loopback API server, got a successful request")
	}
	if !strings.Contains(err.Error(), "SSRF dial refused") {
		t.Fatalf("expected an SSRF dial refusal, got: %v", err)
	}
}
