package servicemesh

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// unstructuredFromFuzz decodes fuzz bytes into an *unstructured.Unstructured.
// Inputs that don't decode to a JSON/YAML object are skipped — the seed corpus
// carries the structural diversity and the mutator explores around it.
func unstructuredFromFuzz(data []byte) (*unstructured.Unstructured, bool) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return &unstructured.Unstructured{Object: m}, true
}

// FuzzServiceMeshNormalizers asserts every Istio/Linkerd normalizer is
// crash-safe on arbitrary/adversarial unstructured input. Oracle: no panic;
// returned zero-values are fine. These run inside HTTP handlers (chi-recovered)
// but a panic is still a reliability defect we want surfaced and guarded.
func FuzzServiceMeshNormalizers(f *testing.F) {
	// ---- Realistic valid seeds ----

	// Istio VirtualService
	f.Add([]byte(`
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: reviews
  namespace: bookinfo
spec:
  hosts:
    - reviews
  http:
    - route:
        - destination:
            host: reviews
            subset: v1
          weight: 80
        - destination:
            host: reviews
            subset: v2
            port:
              number: 9080
          weight: 20
`))

	// Istio DestinationRule
	f.Add([]byte(`
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: reviews-dr
  namespace: bookinfo
spec:
  host: reviews
  subsets:
    - name: v1
      labels:
        version: v1
    - name: v2
      labels:
        version: v2
`))

	// Istio Gateway
	f.Add([]byte(`
apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: bookinfo-gateway
  namespace: istio-system
spec:
  selector:
    istio: ingressgateway
  servers:
    - port:
        number: 80
        name: http
        protocol: HTTP
      hosts:
        - bookinfo.example.com
        - "*.example.com"
`))

	// Istio AuthorizationPolicy
	f.Add([]byte(`
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: allow-frontend
  namespace: bookinfo
spec:
  selector:
    matchLabels:
      app: reviews
  action: ALLOW
  rules:
    - from:
        - source:
            principals:
              - cluster.local/ns/bookinfo/sa/frontend
`))

	// Istio PeerAuthentication
	f.Add([]byte(`
apiVersion: security.istio.io/v1beta1
kind: PeerAuthentication
metadata:
  name: default
  namespace: bookinfo
spec:
  selector:
    matchLabels:
      app: reviews
  mtls:
    mode: STRICT
`))

	// Linkerd ServiceProfile
	f.Add([]byte(`
apiVersion: linkerd.io/v1alpha2
kind: ServiceProfile
metadata:
  name: reviews.bookinfo.svc.cluster.local
  namespace: bookinfo
spec:
  routes:
    - name: GET /reviews
      condition:
        method: GET
        pathRegex: /reviews/[^/]*
    - name: POST /reviews
      condition:
        method: POST
        pathRegex: /reviews
`))

	// Linkerd Server
	f.Add([]byte(`
apiVersion: policy.linkerd.io/v1beta1
kind: Server
metadata:
  name: reviews-server
  namespace: bookinfo
spec:
  podSelector:
    matchLabels:
      app: reviews
  port: 9080
  proxyProtocol: HTTP/2
`))

	// Linkerd HTTPRoute
	f.Add([]byte(`
apiVersion: policy.linkerd.io/v1beta2
kind: HTTPRoute
metadata:
  name: reviews-route
  namespace: bookinfo
spec:
  parentRefs:
    - name: reviews-server
      kind: Server
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /reviews
          method: GET
`))

	// Linkerd AuthorizationPolicy
	f.Add([]byte(`
apiVersion: policy.linkerd.io/v1alpha1
kind: AuthorizationPolicy
metadata:
  name: allow-frontend
  namespace: bookinfo
spec:
  targetRef:
    group: policy.linkerd.io
    kind: Server
    name: reviews-server
  requiredAuthenticationRefs:
    - name: frontend-auth
      kind: MeshTLSAuthentication
`))

	// Linkerd MeshTLSAuthentication
	f.Add([]byte(`
apiVersion: policy.linkerd.io/v1alpha1
kind: MeshTLSAuthentication
metadata:
  name: frontend-auth
  namespace: bookinfo
spec:
  identities:
    - "frontend.bookinfo.serviceaccount.identity.linkerd.cluster.local"
    - "*.bookinfo.serviceaccount.identity.linkerd.cluster.local"
`))

	// ---- Malformed / adversarial seeds ----

	// Empty object
	f.Add([]byte(`{}`))

	// metadata is a scalar, not a map
	f.Add([]byte(`{"metadata":"oops"}`))

	// spec is a list and status is a scalar
	f.Add([]byte(`{"spec":[],"status":"x"}`))

	// spec.http is a scalar instead of a list
	f.Add([]byte(`{"spec":{"http":"notalist"}}`))

	// spec.selector.matchLabels is a list instead of a map
	f.Add([]byte(`{"spec":{"selector":{"matchLabels":[]}}}`))

	// Deeply nested but wrong types at every level
	f.Add([]byte(`{
		"spec": {
			"hosts": "not-a-list",
			"gateways": 42,
			"subsets": "wrong",
			"servers": "wrong",
			"routes": "wrong",
			"parentRefs": "wrong",
			"rules": "wrong",
			"identities": "wrong",
			"targetRef": "wrong",
			"requiredAuthenticationRefs": "wrong",
			"mtls": "wrong",
			"action": 99
		}
	}`))

	// route destination port as a string instead of int
	f.Add([]byte(`{
		"spec": {
			"http": [{"route": [{"destination": {"host": "svc", "port": {"number": "not-an-int"}}, "weight": "heavy"}]}]
		}
	}`))

	// spec.servers[*].hosts as a map instead of a list
	f.Add([]byte(`{"spec":{"servers":[{"hosts":{"key":"val"}}]}}`))

	// spec.subsets has non-map elements
	f.Add([]byte(`{"spec":{"subsets":["string-not-map",42,null]}}`))

	// Null values at every spec key
	f.Add([]byte(`{"spec":null,"metadata":null,"status":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		u, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}

		// ---- Istio dispatch functions (cover all switch branches + default) ----

		// normalizeIstioRoute: VirtualService, DestinationRule, Gateway branches + unknown
		_ = normalizeIstioRoute(u, "VirtualService")
		_ = normalizeIstioRoute(u, "DestinationRule")
		_ = normalizeIstioRoute(u, "Gateway")
		_ = normalizeIstioRoute(u, "UnknownIstioKind")

		// normalizeIstioVirtualService (also exercised by route dispatch above)
		_ = normalizeIstioVirtualService(u)

		// normalizeIstioDestinationRule
		_ = normalizeIstioDestinationRule(u)

		// normalizeIstioGateway
		_ = normalizeIstioGateway(u)

		// normalizeIstioPolicy: PeerAuthentication, AuthorizationPolicy branches + unknown
		_ = normalizeIstioPolicy(u, "PeerAuthentication")
		_ = normalizeIstioPolicy(u, "AuthorizationPolicy")
		_ = normalizeIstioPolicy(u, "UnknownIstioPolicy")

		// normalizeIstioPeerAuth (also exercised by policy dispatch above)
		_ = normalizeIstioPeerAuth(u)

		// normalizeIstioAuthzPolicy
		_ = normalizeIstioAuthzPolicy(u)

		// ---- Linkerd dispatch functions (cover all switch branches + default) ----

		// normalizeLinkerdRoute: ServiceProfile, Server, HTTPRoute branches + unknown
		_ = normalizeLinkerdRoute(u, "ServiceProfile")
		_ = normalizeLinkerdRoute(u, "Server")
		_ = normalizeLinkerdRoute(u, "HTTPRoute")
		_ = normalizeLinkerdRoute(u, "UnknownLinkerdKind")

		// normalizeServiceProfile (also exercised by route dispatch above)
		_ = normalizeServiceProfile(u)

		// normalizeLinkerdServer
		_ = normalizeLinkerdServer(u)

		// normalizeLinkerdHTTPRoute
		_ = normalizeLinkerdHTTPRoute(u)

		// normalizeLinkerdPolicy: AuthorizationPolicy, MeshTLSAuthentication branches + unknown
		_ = normalizeLinkerdPolicy(u, "AuthorizationPolicy")
		_ = normalizeLinkerdPolicy(u, "MeshTLSAuthentication")
		_ = normalizeLinkerdPolicy(u, "UnknownLinkerdPolicy")

		// normalizeLinkerdAuthzPolicy (also exercised by policy dispatch above)
		_ = normalizeLinkerdAuthzPolicy(u)

		// normalizeLinkerdMeshTLSAuth
		_ = normalizeLinkerdMeshTLSAuth(u)
	})
}
