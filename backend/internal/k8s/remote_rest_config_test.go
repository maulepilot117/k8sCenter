package k8s

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
)

// TestRemoteRESTConfigIsTheOnlyConfigLiteral guards the call sites the
// dialer tests cannot reach: ProbeOne needs a PostgreSQL ClusterStore, and
// the registration handler needs a live remote API server. Before R-4 both
// built a rest.Config literal inline and silently lacked the strict dialer.
// Local configs come from rest.InClusterConfig / clientcmd, so the only
// literal in production code should be the one in NewRemoteRESTConfig.
func TestRemoteRESTConfigIsTheOnlyConfigLiteral(t *testing.T) {
	const allowed = "k8s/cluster_router.go"
	root := filepath.Join("..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n := strings.Count(string(src), "rest.Config{")
		if filepath.ToSlash(path) == "../"+allowed {
			n--
		}
		if n > 0 {
			offenders = append(offenders, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(offenders) > 0 {
		t.Fatalf("rest.Config literal outside NewRemoteRESTConfig in %v: build remote configs with NewRemoteRESTConfig so they keep the strict dialer", offenders)
	}
}

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

// TestNewRemoteRESTConfig_BypassesEnvironmentProxy pins the other half of
// R-4. With a nil Proxy, client-go routes through http.ProxyFromEnvironment:
// the strict dialer then checks the proxy's address, and the proxy resolves
// the API host itself, so a rebound private target is never checked. A
// remote config must connect directly. Setting the env var here is not
// reliable (net/http caches it per process), so the test asks the config's
// own policy for a public API host.
func TestNewRemoteRESTConfig_BypassesEnvironmentProxy(t *testing.T) {
	cfg := NewRemoteRESTConfig("https://api.example.com:6443", "token", nil)
	if cfg.Proxy == nil {
		t.Fatal("Proxy is nil, so client-go falls back to http.ProxyFromEnvironment and the strict dialer checks the proxy instead of the API server")
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com:6443/version", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	proxyURL, err := cfg.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if proxyURL != nil {
		t.Fatalf("expected a direct connection, got proxy %s", proxyURL)
	}
}
