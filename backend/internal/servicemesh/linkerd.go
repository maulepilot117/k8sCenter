package servicemesh

import (
	"context"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// linkerdCRD bundles a GVR with the Kind name used for composite IDs and
// normalizer dispatch. Mirrors istioCRD in istio.go; kept per-mesh to match
// the plan's "per-mesh adapter isolation" principle.
type linkerdCRD struct {
	GVR  schema.GroupVersionResource
	Kind string
}

var (
	// linkerdRouteCRDs are the routing-shaped CRDs normalized into TrafficRoute.
	// HTTPRoute uses the Linkerd-flavored group (policy.linkerd.io), not the
	// upstream gateway.networking.k8s.io CRD — the two coexist on some clusters
	// and the handler must only surface Linkerd's.
	linkerdRouteCRDs = []linkerdCRD{
		{LinkerdServiceProfileGVR, "ServiceProfile"},
		{LinkerdServerGVR, "Server"},
		{LinkerdHTTPRouteGVR, "HTTPRoute"},
	}

	// linkerdPolicyCRDs are the security-shaped CRDs normalized into MeshedPolicy.
	linkerdPolicyCRDs = []linkerdCRD{
		{LinkerdAuthorizationPolicyGVR, "AuthorizationPolicy"},
		{LinkerdMeshTLSAuthenticationGVR, "MeshTLSAuthentication"},
	}
)

// LinkerdListResult carries partial results from a parallel list across all
// Linkerd CRDs. Same shape as IstioListResult so the handler treats both
// meshes symmetrically.
type LinkerdListResult struct {
	Routes   []TrafficRoute
	Policies []MeshedPolicy
	Errors   map[string]string // Kind → error message
}

// ListLinkerd fetches every Linkerd route and policy CRD from the given
// namespace (pass "" for cluster-wide) in parallel. Per-call timeout +
// item cap + partial-error semantics come from the shared listCRD helper.
func ListLinkerd(ctx context.Context, dynClient dynamic.Interface, namespace string) LinkerdListResult {
	result := LinkerdListResult{Errors: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup

	listOpts := metav1.ListOptions{Limit: meshListCap}

	for _, c := range linkerdRouteCRDs {
		wg.Add(1)
		go func(c linkerdCRD) {
			defer wg.Done()
			items, err := listCRD(ctx, dynClient, c.GVR, namespace, listOpts)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors[c.Kind] = err.Error()
				return
			}
			for i := range items {
				result.Routes = append(result.Routes, normalizeLinkerdRoute(&items[i], c.Kind))
			}
		}(c)
	}

	for _, c := range linkerdPolicyCRDs {
		wg.Add(1)
		go func(c linkerdCRD) {
			defer wg.Done()
			items, err := listCRD(ctx, dynClient, c.GVR, namespace, listOpts)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors[c.Kind] = err.Error()
				return
			}
			for i := range items {
				result.Policies = append(result.Policies, normalizeLinkerdPolicy(&items[i], c.Kind))
			}
		}(c)
	}

	wg.Wait()
	return result
}
