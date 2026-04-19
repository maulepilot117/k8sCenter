package servicemesh

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	meshListTimeout = 5 * time.Second
	meshListCap     = 2000
)

// istioCRD bundles a GVR with the Kind name used for composite IDs and
// the per-kind normalizer dispatch. Keeping the two together prevents
// the list path and the normalize path from drifting out of sync.
type istioCRD struct {
	GVR  schema.GroupVersionResource
	Kind string
}

var (
	// istioRouteCRDs are the routing-shaped CRDs normalized into TrafficRoute.
	istioRouteCRDs = []istioCRD{
		{IstioVirtualServiceGVR, "VirtualService"},
		{IstioDestinationRuleGVR, "DestinationRule"},
		{IstioGatewayGVR, "Gateway"},
	}

	// istioPolicyCRDs are the security-shaped CRDs normalized into MeshedPolicy.
	istioPolicyCRDs = []istioCRD{
		{IstioPeerAuthenticationGVR, "PeerAuthentication"},
		{IstioAuthorizationPolicyGVR, "AuthorizationPolicy"},
	}
)

// IstioListResult carries partial results from a parallel list across all
// Istio CRDs. Per-CRD errors (e.g., 403 for one kind) are collected in Errors
// so the caller can surface an error annotation without failing the request.
type IstioListResult struct {
	Routes   []TrafficRoute
	Policies []MeshedPolicy
	Errors   map[string]string // Kind → error message
}

// ListIstio fetches every Istio route and policy CRD from the given namespace
// (pass "" for cluster-wide) in parallel. Each list call has a meshListTimeout
// budget and a meshListCap limit; errors for any individual CRD are recorded
// in the result's Errors map so partial data still flows to the UI.
func ListIstio(ctx context.Context, dynClient dynamic.Interface, namespace string) IstioListResult {
	result := IstioListResult{Errors: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup

	listOpts := metav1.ListOptions{Limit: meshListCap}

	for _, c := range istioRouteCRDs {
		wg.Add(1)
		go func(c istioCRD) {
			defer wg.Done()
			items, err := listCRD(ctx, dynClient, c.GVR, namespace, listOpts)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors[c.Kind] = err.Error()
				return
			}
			for i := range items {
				result.Routes = append(result.Routes, normalizeIstioRoute(&items[i], c.Kind))
			}
		}(c)
	}

	for _, c := range istioPolicyCRDs {
		wg.Add(1)
		go func(c istioCRD) {
			defer wg.Done()
			items, err := listCRD(ctx, dynClient, c.GVR, namespace, listOpts)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors[c.Kind] = err.Error()
				return
			}
			for i := range items {
				result.Policies = append(result.Policies, normalizeIstioPolicy(&items[i], c.Kind))
			}
		}(c)
	}

	wg.Wait()
	return result
}

// listCRD performs a single dynamic-client List with a per-call timeout and
// enforces the mesh-wide item cap. It's shared across Istio and (eventually)
// Linkerd adapters because both have the same rate-limit + timeout + cap
// requirements per the plan.
func listCRD(ctx context.Context, dynClient dynamic.Interface, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) ([]unstructured.Unstructured, error) {
	callCtx, cancel := context.WithTimeout(ctx, meshListTimeout)
	defer cancel()

	list, err := dynClient.Resource(gvr).Namespace(namespace).List(callCtx, opts)
	if err != nil {
		return nil, err
	}
	items := list.Items
	if len(items) > meshListCap {
		items = items[:meshListCap]
	}
	return items, nil
}
