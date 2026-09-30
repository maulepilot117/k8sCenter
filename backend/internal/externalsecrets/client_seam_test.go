package externalsecrets

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// recordingClients is a k8s.ClusterClients that records the cluster id each
// resolution asked for. A non-nil err makes every resolution fail.
type recordingClients struct {
	dynFor, typedFor []string
	err              error
}

func (c *recordingClients) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	c.typedFor = append(c.typedFor, id)
	if c.err != nil {
		return nil, c.err
	}
	return kfake.NewSimpleClientset(), nil
}

func (c *recordingClients) DynamicClientForCluster(_ context.Context, id, _ string, _ []string) (dynamic.Interface, error) {
	c.dynFor = append(c.dynFor, id)
	if c.err != nil {
		return nil, c.err
	}
	return dynfake.NewSimpleDynamicClient(runtime.NewScheme()), nil
}

func (c *recordingClients) TargetSchemaFor(context.Context, string, string, []string) (*k8s.TargetSchema, error) {
	return nil, errors.New("not used")
}

var seamUser = &auth.User{KubernetesUsername: "alice", KubernetesGroups: []string{"devs"}}

// The per-user clients resolve on the cluster the request names. This
// process's own configured id resolves as local, because ClusterRouter knows
// that cluster only as "local".
func TestRequestClients_ResolveRequestCluster(t *testing.T) {
	cases := []struct {
		name       string
		selfID     string
		ctxCluster string // "" leaves the context without a cluster id
		want       string
	}{
		{"no cluster in context", "", "", k8s.LocalClusterID},
		{"local", "", k8s.LocalClusterID, k8s.LocalClusterID},
		{"remote", "", "remote-1", "remote-1"},
		{"configured self id", "homelab", "homelab", k8s.LocalClusterID},
		{"remote with a configured self id", "homelab", "remote-1", "remote-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clients := &recordingClients{}
			h := &Handler{Clients: clients, ClusterID: tc.selfID}
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
	h := &Handler{Clients: &recordingClients{err: wantErr}}
	ctx := middleware.WithClusterID(context.Background(), "remote-1")

	if c, err := h.dynForRequest(ctx, seamUser); !errors.Is(err, wantErr) || c != nil {
		t.Errorf("dynForRequest = (%v, %v), want (nil, %v)", c, err, wantErr)
	}
	if c, err := h.clientForRequest(ctx, seamUser); !errors.Is(err, wantErr) || c != nil {
		t.Errorf("clientForRequest = (%v, %v), want (nil, %v)", c, err, wantErr)
	}
}
