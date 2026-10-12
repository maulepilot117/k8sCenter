package k8s

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/portforward"
	streamhttp "k8s.io/streaming/pkg/httpstream"
	streamspdy "k8s.io/streaming/pkg/httpstream/spdy"
)

// fakePortForwardServer answers a pods/portforward upgrade the way the API
// server does: it negotiates the port-forward protocol, records each stream's
// headers in creation order, and echoes the data stream. With errMessage set,
// it writes that message on the error stream and ends the data stream, as
// the kubelet does when the pod refuses the port.
type fakePortForwardServer struct {
	*httptest.Server
	errMessage string

	mu      sync.Mutex
	paths   []string
	streams []http.Header
}

func newFakePortForwardServer(t *testing.T, errMessage string) *fakePortForwardServer {
	t.Helper()
	fs := &fakePortForwardServer{errMessage: errMessage}
	fs.Server = httptest.NewTLSServer(http.HandlerFunc(fs.serve))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *fakePortForwardServer) serve(w http.ResponseWriter, r *http.Request) {
	fs.mu.Lock()
	fs.paths = append(fs.paths, r.URL.Path)
	fs.mu.Unlock()
	if _, err := streamhttp.Handshake(r, w, []string{portforward.PortForwardProtocolV1Name}); err != nil {
		return
	}
	streams := make(chan streamhttp.Stream, 8)
	conn := streamspdy.NewResponseUpgrader().UpgradeResponse(w, r, func(s streamhttp.Stream, _ <-chan struct{}) error {
		streams <- s
		return nil
	})
	if conn == nil {
		return
	}
	defer conn.Close()
	for {
		select {
		case s := <-streams:
			fs.mu.Lock()
			fs.streams = append(fs.streams, s.Headers().Clone())
			fs.mu.Unlock()
			go fs.handle(s)
		case <-conn.CloseChan():
			return
		}
	}
}

func (fs *fakePortForwardServer) handle(s streamhttp.Stream) {
	switch s.Headers().Get(corev1.StreamType) {
	case corev1.StreamTypeError:
		if fs.errMessage != "" {
			_, _ = s.Write([]byte(fs.errMessage))
		}
		_ = s.Close()
	case corev1.StreamTypeData:
		if fs.errMessage != "" {
			_ = s.Close()
			return
		}
		_, _ = io.Copy(s, s)
		_ = s.Close()
	}
}

func (fs *fakePortForwardServer) recorded() ([]string, []http.Header) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]string(nil), fs.paths...), append([]http.Header(nil), fs.streams...)
}

func openTunnel(t *testing.T, srv *httptest.Server) Tunnel {
	t.Helper()
	st := testStreamTransport(srv.URL, serverCAPEM(srv), false, plainDial)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tun, err := st.PortForward(ctx, "kube-system", "hubble-relay-abcde", 4245)
	if err != nil {
		t.Fatalf("PortForward: %v", err)
	}
	t.Cleanup(func() { _ = tun.Close() })
	return tun
}

func TestStreamTransport_PortForwardOpensErrorThenDataStream(t *testing.T) {
	srv := newFakePortForwardServer(t, "")
	tun := openTunnel(t, srv.Server)

	conn, err := tun.DialContext(context.Background())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v; want the echoed ping", buf, err)
	}

	paths, streams := srv.recorded()
	if len(paths) != 1 || paths[0] != "/api/v1/namespaces/kube-system/pods/hubble-relay-abcde/portforward" {
		t.Errorf("upgrade paths = %v, want the pod's portforward subresource", paths)
	}
	if len(streams) != 2 {
		t.Fatalf("server saw %d streams, want 2", len(streams))
	}
	if got := streams[0].Get(corev1.StreamType); got != corev1.StreamTypeError {
		t.Errorf("first stream type = %q, want the error stream", got)
	}
	if got := streams[1].Get(corev1.StreamType); got != corev1.StreamTypeData {
		t.Errorf("second stream type = %q, want the data stream", got)
	}
	for i, h := range streams {
		if got := h.Get(corev1.PortHeader); got != "4245" {
			t.Errorf("stream %d port = %q, want 4245", i, got)
		}
	}
	if a, b := streams[0].Get(corev1.PortForwardRequestIDHeader), streams[1].Get(corev1.PortForwardRequestIDHeader); a == "" || a != b {
		t.Errorf("request ids = %q and %q, want one shared id", a, b)
	}
}

func TestStreamTransport_PortForwardErrorStreamIsSanitized(t *testing.T) {
	const uid = "7f3c9d2e-1b4a-4c8e-9f60-2d5e8a7b3c11"
	srv := newFakePortForwardServer(t, "an error occurred forwarding 4245 -> 4245: error forwarding port 4245 to pod "+uid+", uid : connection refused")
	tun := openTunnel(t, srv.Server)

	conn, err := tun.DialContext(context.Background())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	_, err = conn.Read(make([]byte, 16))
	if !errors.Is(err, ErrPortForwardFailed) {
		t.Fatalf("Read err = %v, want ErrPortForwardFailed", err)
	}
	if strings.Contains(err.Error(), uid) {
		t.Errorf("error %q carries the pod uid", err)
	}
}

func TestStreamTransport_PortForwardRefusalKeepsStatus(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"plain text body", "forbidden"},
		{"status body", `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"pods \"hubble-relay-abcde\" is forbidden","reason":"Forbidden","code":403}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			st := testStreamTransport(srv.URL, serverCAPEM(srv), false, plainDial)
			_, err := st.PortForward(context.Background(), "kube-system", "hubble-relay-abcde", 4245)
			if !apierrors.IsForbidden(err) {
				t.Fatalf("err = %v, want a forbidden API error", err)
			}
		})
	}
}

func TestStreamTransport_PortForwardRedirectNotFollowed(t *testing.T) {
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
	if _, err := st.PortForward(context.Background(), "kube-system", "hubble-relay-abcde", 4245); err == nil {
		t.Fatal("port-forward answered by a redirect succeeded")
	}
	if n := followed.Load(); n != 0 {
		t.Errorf("redirect target was requested %d times, want 0", n)
	}
}

func TestStreamTransport_PortForwardRejectsBadTargets(t *testing.T) {
	var dials atomic.Int32
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		return plainDial(ctx, network, addr)
	}
	st := testStreamTransport("https://203.0.113.10:6443", nil, true, dial)
	if _, err := st.PortForward(context.Background(), "kube-system", "../../nodes/x", 4245); err == nil {
		t.Error("port-forward to a path-shaped pod name succeeded")
	}
	if _, err := st.PortForward(context.Background(), "kube-system", "hubble-relay-abcde", 0); err == nil {
		t.Error("port-forward to port 0 succeeded")
	}
	if n := dials.Load(); n != 0 {
		t.Errorf("dialed %d times, want 0", n)
	}
}

func TestStreamTransport_ClosedTunnelFailsDialAtOnce(t *testing.T) {
	baseline := runtime.NumGoroutine()
	srv := newFakePortForwardServer(t, "")
	st := testStreamTransport(srv.URL, serverCAPEM(srv.Server), false, plainDial)
	tun, err := st.PortForward(context.Background(), "kube-system", "hubble-relay-abcde", 4245)
	if err != nil {
		t.Fatalf("PortForward: %v", err)
	}
	conn, err := tun.DialContext(context.Background())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = conn.Close()
	_ = conn.Close() // idempotent
	if err := tun.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-tun.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed after Close")
	}
	start := time.Now()
	if _, err := tun.DialContext(context.Background()); !errors.Is(err, ErrTunnelClosed) {
		t.Fatalf("dial after close: err = %v, want ErrTunnelClosed", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("dial after close took %v, want an immediate failure", elapsed)
	}

	srv.Close()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d after close, baseline %d", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
