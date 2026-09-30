package externalsecrets

import (
	"context"
	"errors"
	"sync"
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// stubClients is a k8s.ClusterClients that serves the same clients for every
// cluster and records the cluster id each resolution asked for. A non-nil
// dynErr or kubeErr fails that resolution. Handler tests wire it as
// Handler.Clients so every per-user read goes through the request seam.
type stubClients struct {
	dyn     dynamic.Interface
	kube    kubernetes.Interface
	dynErr  error
	kubeErr error

	mu               sync.Mutex
	dynFor, typedFor []string
}

func (c *stubClients) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.typedFor = append(c.typedFor, id)
	if c.kubeErr != nil {
		return nil, c.kubeErr
	}
	return c.kube, nil
}

func (c *stubClients) DynamicClientForCluster(_ context.Context, id, _ string, _ []string) (dynamic.Interface, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dynFor = append(c.dynFor, id)
	if c.dynErr != nil {
		return nil, c.dynErr
	}
	return c.dyn, nil
}

func (c *stubClients) TargetSchemaFor(context.Context, string, string, []string) (*k8s.TargetSchema, error) {
	return nil, errors.New("not used")
}

// stubOf returns the stubClients a test handler was built with.
func stubOf(h *Handler) *stubClients {
	return h.Clients.(*stubClients)
}

var seamUser = &auth.User{KubernetesUsername: "alice", KubernetesGroups: []string{"devs"}}

// The per-user clients resolve on the cluster the request names.
func TestRequestClients_ResolveRequestCluster(t *testing.T) {
	cases := []struct {
		name       string
		ctxCluster string // "" leaves the context without a cluster id
		want       string
	}{
		{"no cluster in context", "", k8s.LocalClusterID},
		{"local", k8s.LocalClusterID, k8s.LocalClusterID},
		{"remote", "remote-1", "remote-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clients := &stubClients{}
			h := &Handler{Clients: clients}
			ctx := context.Background()
			if tc.ctxCluster != "" {
				ctx = middleware.WithClusterID(ctx, tc.ctxCluster)
			}

			if _, err := h.dynForRequest(ctx, seamUser); err != nil {
				t.Fatalf("dynForRequest: %v", err)
			}
			if _, err := h.clientForRequest(ctx, seamUser); err != nil {
				t.Fatalf("clientForRequest: %v", err)
			}
			if len(clients.dynFor) != 1 || clients.dynFor[0] != tc.want {
				t.Errorf("dynamic client resolved for %v, want [%s]", clients.dynFor, tc.want)
			}
			if len(clients.typedFor) != 1 || clients.typedFor[0] != tc.want {
				t.Errorf("typed client resolved for %v, want [%s]", clients.typedFor, tc.want)
			}
		})
	}
}

// A failed resolution is returned to the caller, never answered with another
// cluster's client.
func TestRequestClients_ResolutionErrorIsReturned(t *testing.T) {
	wantErr := errors.New("cluster unreachable")
	h := &Handler{Clients: &stubClients{dynErr: wantErr, kubeErr: wantErr}}
	ctx := middleware.WithClusterID(context.Background(), "remote-1")

	if c, err := h.dynForRequest(ctx, seamUser); !errors.Is(err, wantErr) || c != nil {
		t.Errorf("dynForRequest = (%v, %v), want (nil, %v)", c, err, wantErr)
	}
	if c, err := h.clientForRequest(ctx, seamUser); !errors.Is(err, wantErr) || c != nil {
		t.Errorf("clientForRequest = (%v, %v), want (nil, %v)", c, err, wantErr)
	}
}
