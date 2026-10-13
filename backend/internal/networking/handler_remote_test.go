package networking

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// Remote REST flows (#608 slice 5, U10). The handler opens one flow source
// per request, through the fakes in relay_tunnel_test.go.

const remoteFlowsPath = "/api/v1/networking/hubble/flows?namespace=default"

func getRemoteFlows(env *flowTestEnv, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	env.h.HandleHubbleFlows(w, req)
	return w
}

func remoteFlowsRequest() *http.Request {
	return networkingRequest(http.MethodGet, remoteFlowsPath, netRemoteCluster)
}

// trackTunnels records every tunnel the env opens.
func trackTunnels(env *flowTestEnv) *[]*fakeTunnel {
	var opened []*fakeTunnel
	env.tunnels.tunnel = func() k8s.Tunnel {
		tun := newFakeTunnel(func(ctx context.Context) (net.Conn, error) { return env.relay.lis.DialContext(ctx) })
		opened = append(opened, tun)
		return tun
	}
	return &opened
}

func TestHandleHubbleFlows_RemoteServesRelayFlowsWithoutLocalClient(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	opened := trackTunnels(env)
	if env.h.HubbleClient != nil {
		t.Fatal("test handler has a local Hubble client")
	}

	w := getRemoteFlows(env, remoteFlowsRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	var body struct {
		Data []FlowRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	if len(body.Data) != 1 || body.Data[0].SrcPod != "web-1" {
		t.Errorf("flows = %+v, want the remote relay's flow", body.Data)
	}
	if len(*opened) != 1 || !(*opened)[0].closed.Load() {
		t.Errorf("tunnels = %d, closed after the response = %v; want one, closed", len(*opened), len(*opened) == 1 && (*opened)[0].closed.Load())
	}
}

func TestHandleHubbleFlows_RemotePortForwardForbidden(t *testing.T) {
	// AE2: the user may list pods in default but not port-forward to the relay.
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	env.tunnels.err = apierrors.NewForbidden(schema.GroupResource{Resource: "pods/portforward"}, "hubble-relay-a", errors.New("denied"))

	w := getRemoteFlows(env, remoteFlowsRequest())
	if w.Code != http.StatusForbidden || errorReason(t, w) != "forbidden" {
		t.Fatalf("status = %d, body %s; want 403 forbidden", w.Code, w.Body.String())
	}
}

func TestHandleHubbleFlows_RemoteNamespaceDeniedBeforeTunnel(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	env.access.allowed = false

	w := getRemoteFlows(env, remoteFlowsRequest())
	if w.Code != http.StatusForbidden || errorReason(t, w) != "forbidden" {
		t.Fatalf("status = %d, body %s; want 403 forbidden", w.Code, w.Body.String())
	}
	if n := len(env.tunnels.opened()); n != 0 {
		t.Errorf("opened %d tunnels, want none", n)
	}
}

func TestHandleHubbleFlows_RemoteUnreachableIs502(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	env.h.Clients.(*fakeNetClients).targetErr = &net.DNSError{Err: "no such host", Name: "api.remote.example"}

	w := getRemoteFlows(env, remoteFlowsRequest())
	if w.Code != http.StatusBadGateway || errorReason(t, w) != string(k8s.ReasonUnreachable) {
		t.Fatalf("status = %d, body %s; want 502 unreachable", w.Code, w.Body.String())
	}
}

func TestHandleHubbleFlows_RemoteSourceClosedWhenClientCancels(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	opened := trackTunnels(env)
	env.relay.hold = true

	req := remoteFlowsRequest()
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- getRemoteFlows(env, req) }()

	// Cancel once the request is reading flows from an open tunnel.
	deadline := time.Now().Add(5 * time.Second)
	for env.relay.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("request never reached the relay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the client cancelled")
	}
	if len(*opened) != 1 || !(*opened)[0].closed.Load() {
		t.Error("tunnel not closed after a cancelled request")
	}
}

func TestHandleHubbleFlows_RemoteRESTTunnelLimit(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	for range maxRESTTunnelsPerCluster {
		release, ok := env.h.slots.acquire(context.Background(), FlowSourceREST, netRemoteCluster, "someone")
		if !ok {
			t.Fatal("could not fill the REST slots")
		}
		defer release()
	}

	w := getRemoteFlows(env, remoteFlowsRequest())
	if w.Code != http.StatusTooManyRequests || errorReason(t, w) != ReasonTunnelLimit {
		t.Fatalf("status = %d, body %s; want 429 tunnel_limit", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Extra struct {
				Retryable bool `json:"retryable"`
			} `json:"extra"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !body.Error.Extra.Retryable {
		t.Errorf("body = %s, want retryable: true", w.Body.String())
	}
}
