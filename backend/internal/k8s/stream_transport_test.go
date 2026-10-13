package k8s

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// These tests drive StreamTransport.Exec against httptest servers. The
// production upgrade dialer is StrictDialContext, which refuses loopback, so
// the tests reach their servers through the package-internal dial field and
// pin separately that production never uses anything else.

var plainDial = (&net.Dialer{}).DialContext

// recordingServer answers every request with 403 after recording it, so an
// exec reaches the server and fails without a real SPDY upgrade.
type recordingServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.reqs = append(rs.reqs, r.Clone(context.Background()))
		rs.mu.Unlock()
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingServer) requests() []*http.Request {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]*http.Request(nil), rs.reqs...)
}

func serverCAPEM(srv *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}

// otherCAPEM returns a self-signed CA certificate that did not sign the
// httptest server's certificate.
func otherCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unrelated test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// testStreamTransport builds a transport the way StreamTransportFor does,
// from NewRemoteRESTConfig plus impersonation, with the given dialer.
func testStreamTransport(host string, caData []byte, insecure bool, dial func(context.Context, string, string) (net.Conn, error)) *StreamTransport {
	cfg := NewRemoteRESTConfig(host, "stored-cluster-token", caData)
	cfg.TLSClientConfig.Insecure = insecure
	cfg.Impersonate = rest.ImpersonationConfig{UserName: "alice", Groups: []string{"engineering", "oncall"}}
	return &StreamTransport{cfg: cfg, dial: dial}
}

func execStatus(st *StreamTransport) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return st.Exec(ctx, "kube-system", "cilium-abcde", ExecCiliumStatusJSON, io.Discard, io.Discard)
}

func TestStreamTransportFor_ProductionDialIsStrict(t *testing.T) {
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})
	st, err := router.StreamTransportFor(context.Background(), "remote-1", "alice", []string{"engineering"})
	if err != nil {
		t.Fatalf("StreamTransportFor: %v", err)
	}
	if reflect.ValueOf(st.dial).Pointer() != reflect.ValueOf(StrictDialContext).Pointer() {
		t.Fatal("production stream transport does not dial through StrictDialContext")
	}
	if st.cfg.Impersonate.UserName != "alice" {
		t.Errorf("Impersonate.UserName = %q, want alice", st.cfg.Impersonate.UserName)
	}
}

func TestStreamTransportFor_RefusesLocalAndAnonymous(t *testing.T) {
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})
	ctx := context.Background()
	for _, id := range []string{"", "local"} {
		if _, err := router.StreamTransportFor(ctx, id, "alice", nil); err == nil {
			t.Errorf("StreamTransportFor(%q) built a transport for the local cluster", id)
		}
	}
	if _, err := router.StreamTransportFor(ctx, "remote-1", "", []string{"engineering"}); err == nil {
		t.Error("StreamTransportFor built a transport with no user to impersonate")
	}
}

func TestStreamTransport_StrictDialRefusesPrivateTargets(t *testing.T) {
	srv := newRecordingServer(t)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	for _, host := range []string{"https://127.0.0.1:" + port, "https://10.0.0.1:6443"} {
		st := testStreamTransport(host, serverCAPEM(srv.Server), false, StrictDialContext)
		err := execStatus(st)
		if err == nil || !strings.Contains(err.Error(), "SSRF dial refused") {
			t.Errorf("exec to %s: err = %v, want an SSRF refusal", host, err)
		}
	}
	if n := len(srv.requests()); n != 0 {
		t.Errorf("server received %d requests, want none", n)
	}
}

func TestStreamTransport_UpgradeCarriesImpersonation(t *testing.T) {
	srv := newRecordingServer(t)
	st := testStreamTransport(srv.URL, serverCAPEM(srv.Server), false, plainDial)
	if err := execStatus(st); err == nil {
		t.Fatal("exec against a 403 server succeeded")
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("server received %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if got := r.Header.Get("Impersonate-User"); got != "alice" {
		t.Errorf("Impersonate-User = %q, want alice", got)
	}
	if got := r.Header.Values("Impersonate-Group"); !reflect.DeepEqual(sortedCopy(got), []string{"engineering", "oncall"}) {
		t.Errorf("Impersonate-Group = %v, want [engineering oncall]", got)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer stored-cluster-token" {
		t.Errorf("Authorization = %q, want the stored cluster token", got)
	}
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/namespaces/kube-system/pods/cilium-abcde/exec" {
		t.Errorf("request = %s %s, want POST to the pod's exec subresource", r.Method, r.URL.Path)
	}
	q := r.URL.Query()
	if q.Get("container") != "cilium-agent" || !reflect.DeepEqual(q["command"], []string{"cilium-dbg", "status", "-o", "json"}) {
		t.Errorf("query = %v, want the fixed cilium-agent status command", q)
	}
}

func TestStreamTransport_RedirectNotFollowed(t *testing.T) {
	var followed atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/elsewhere", func(w http.ResponseWriter, _ *http.Request) {
		followed.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	st := testStreamTransport(srv.URL, serverCAPEM(srv), false, plainDial)
	if err := execStatus(st); err == nil {
		t.Fatal("exec answered by a redirect succeeded")
	}
	if n := followed.Load(); n != 0 {
		t.Errorf("redirect target was requested %d times, want 0", n)
	}
}

func TestStreamTransport_TLSPolicy(t *testing.T) {
	cases := []struct {
		name        string
		caData      func(*testing.T, *httptest.Server) []byte
		insecure    bool
		wantReached bool
	}{
		{"cluster CA trusted", func(_ *testing.T, s *httptest.Server) []byte { return serverCAPEM(s) }, false, true},
		{"unrelated CA refused", func(t *testing.T, _ *httptest.Server) []byte { return otherCAPEM(t) }, false, false},
		// No CA and no opt-in must still verify (system roots), never
		// fall back to InsecureSkipVerify.
		{"no CA verifies against system roots", func(*testing.T, *httptest.Server) []byte { return nil }, false, false},
		// The operator's AllowInsecureTLS opt-in behaves as it does for
		// the cluster's REST client.
		{"AllowInsecureTLS opt-in", func(*testing.T, *httptest.Server) []byte { return nil }, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRecordingServer(t)
			st := testStreamTransport(srv.URL, tc.caData(t, srv.Server), tc.insecure, plainDial)
			_ = execStatus(st)
			if reached := len(srv.requests()) > 0; reached != tc.wantReached {
				t.Errorf("request reached server = %v, want %v", reached, tc.wantReached)
			}
		})
	}
}

func TestStreamTransport_RejectsCommandOutsideFixedSet(t *testing.T) {
	var dials atomic.Int32
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		return plainDial(ctx, network, addr)
	}
	st := testStreamTransport("https://203.0.113.10:6443", nil, true, dial)
	err := st.Exec(context.Background(), "kube-system", "cilium-abcde", ExecCommand(99), io.Discard, io.Discard)
	if err == nil {
		t.Fatal("exec of an undefined command succeeded")
	}
	if err := st.Exec(context.Background(), "kube-system", "../../nodes/x", ExecCiliumStatusJSON, io.Discard, io.Discard); err == nil {
		t.Error("exec with a path-shaped pod name succeeded")
	}
	if n := dials.Load(); n != 0 {
		t.Errorf("dialed %d times, want 0", n)
	}
}

func TestStreamTransport_StalledDialHitsDeadline(t *testing.T) {
	baseline := runtime.NumGoroutine()
	stall := func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	st := testStreamTransport("https://203.0.113.10:6443", nil, true, stall)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := st.Exec(ctx, "kube-system", "cilium-abcde", ExecCiliumStatusJSON, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("exec returned after %v, want it bounded by the context deadline", elapsed)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d after exec, baseline %d", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
