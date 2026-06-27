package gateway

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

// FuzzGatewayNormalizers asserts every Gateway API normalizer is crash-safe on
// arbitrary/adversarial unstructured input. Oracle: no panic; zero-values fine.
func FuzzGatewayNormalizers(f *testing.F) {
	// ---- Realistic valid seeds ----

	// GatewayClass
	f.Add([]byte(`
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: nginx
spec:
  controllerName: k8s.nginx.org/nginx-gateway-controller
  description: NGINX Gateway Fabric
status:
  conditions:
    - type: Accepted
      status: "True"
      reason: Accepted
      message: GatewayClass is accepted
      lastTransitionTime: "2024-01-01T00:00:00Z"
`))

	// Gateway with listeners, addresses, and status conditions
	f.Add([]byte(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: prod-gw
  namespace: infra
spec:
  gatewayClassName: nginx
  listeners:
    - name: http
      port: 80
      protocol: HTTP
      hostname: "*.example.com"
      allowedRoutes:
        namespaces:
          from: All
        kinds:
          - kind: HTTPRoute
            group: gateway.networking.k8s.io
    - name: https
      port: 443
      protocol: HTTPS
      hostname: "*.example.com"
      tls:
        mode: Terminate
        certificateRefs:
          - name: example-cert
            namespace: infra
      allowedRoutes:
        namespaces:
          from: Same
status:
  addresses:
    - type: IPAddress
      value: 10.0.0.1
    - type: Hostname
      value: prod-gw.infra.svc.cluster.local
  conditions:
    - type: Programmed
      status: "True"
      reason: Programmed
      lastTransitionTime: "2024-01-01T00:00:00Z"
  listeners:
    - name: http
      attachedRoutes: 3
      conditions:
        - type: ResolvedRefs
          status: "True"
          reason: ResolvedRefs
    - name: https
      attachedRoutes: 1
      conditions:
        - type: ResolvedRefs
          status: "True"
          reason: ResolvedRefs
`))

	// HTTPRoute with matches, backendRefs, parentRefs
	f.Add([]byte(`
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: api-route
  namespace: default
spec:
  parentRefs:
    - name: prod-gw
      namespace: infra
      sectionName: https
  hostnames:
    - api.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /v1
          method: GET
          headers:
            - name: X-Custom
              value: enabled
          queryParams:
            - name: debug
              value: "true"
      filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            set:
              - name: X-Forwarded-Host
                value: api.example.com
            add:
              - name: X-Request-ID
                value: generated
            remove:
              - X-Internal-Token
        - type: RequestRedirect
          requestRedirect:
            scheme: https
            hostname: api-v2.example.com
            statusCode: 301
        - type: URLRewrite
          urlRewrite:
            hostname: upstream.internal
            path:
              type: ReplacePrefixMatch
              value: /api
        - type: RequestMirror
          requestMirror:
            backendRef:
              name: mirror-svc
        - type: ExtensionRef
          extensionRef:
            group: networking.example.io
            kind: Policy
            name: rate-limit
      backendRefs:
        - name: api-svc
          port: 8080
          weight: 90
        - name: api-svc-canary
          port: 8080
          weight: 10
          namespace: canary
status:
  parents:
    - parentRef:
        name: prod-gw
      conditions:
        - type: Accepted
          status: "True"
          reason: Accepted
          lastTransitionTime: "2024-01-01T00:00:00Z"
        - type: ResolvedRefs
          status: "True"
          reason: ResolvedRefs
`))

	// GRPCRoute with method-based matches
	f.Add([]byte(`
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: GRPCRoute
metadata:
  name: grpc-route
  namespace: default
spec:
  parentRefs:
    - name: prod-gw
      namespace: infra
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - method:
            service: io.example.UserService
            method: GetUser
          headers:
            - name: authorization
              value: Bearer
    - matches:
        - method:
            service: io.example.OrderService
      backendRefs:
        - name: order-grpc-svc
          port: 9090
status:
  parents:
    - parentRef:
        name: prod-gw
      conditions:
        - type: Accepted
          status: "True"
          reason: Accepted
`))

	// TCPRoute (simple route type)
	f.Add([]byte(`
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: tcp-route
  namespace: default
spec:
  parentRefs:
    - name: prod-gw
      namespace: infra
      sectionName: tcp
  rules:
    - backendRefs:
        - name: tcp-svc
          port: 5432
          weight: 100
status:
  parents:
    - parentRef:
        name: prod-gw
      conditions:
        - type: Accepted
          status: "True"
          reason: Accepted
`))

	// TLSRoute with hostnames (SimpleRouteDetail populates hostnames for TLSRoute)
	f.Add([]byte(`
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TLSRoute
metadata:
  name: tls-route
  namespace: default
spec:
  parentRefs:
    - name: prod-gw
      namespace: infra
      sectionName: tls-passthrough
  hostnames:
    - db.example.com
  rules:
    - backendRefs:
        - name: db-svc
          port: 5432
status:
  parents:
    - parentRef:
        name: prod-gw
      conditions:
        - type: Accepted
          status: "True"
          reason: Accepted
`))

	// ---- Malformed / adversarial seeds ----

	// Empty object
	f.Add([]byte(`{}`))

	// metadata is a scalar, not a map
	f.Add([]byte(`{"metadata":"oops"}`))

	// spec is a list and status is a scalar
	f.Add([]byte(`{"spec":[],"status":"x"}`))

	// spec.listeners and spec.rules are wrong types
	f.Add([]byte(`{"spec":{"listeners":"notalist","rules":{}}}`))

	// status.conditions and status.addresses are wrong types
	f.Add([]byte(`{"status":{"conditions":{},"addresses":"x"}}`))

	// spec.parentRefs is not a slice
	f.Add([]byte(`{"spec":{"parentRefs":"not-a-slice"}}`))

	// spec.rules elements are not maps
	f.Add([]byte(`{"spec":{"rules":["string",42,null,true]}}`))

	// backendRefs port and weight as strings instead of numbers
	f.Add([]byte(`{
		"spec": {
			"rules": [{"backendRefs": [{"name": "svc", "port": "notint", "weight": "heavy"}]}]
		}
	}`))

	// spec.listeners[*].tls is not a map
	f.Add([]byte(`{"spec":{"listeners":[{"name":"l","tls":"not-a-map"}]}}`))

	// spec.listeners[*].tls.certificateRefs is not a slice
	f.Add([]byte(`{"spec":{"listeners":[{"tls":{"certificateRefs":"notaslice"}}]}}`))

	// spec.listeners[*].allowedRoutes is not a map
	f.Add([]byte(`{"spec":{"listeners":[{"allowedRoutes":"notamap"}]}}`))

	// spec.listeners[*].allowedRoutes.kinds has non-map elements
	f.Add([]byte(`{"spec":{"listeners":[{"allowedRoutes":{"kinds":["string",42,null]}}]}}`))

	// status.listeners[*].attachedRoutes is a string instead of an int
	f.Add([]byte(`{"status":{"listeners":[{"name":"l","attachedRoutes":"many"}]}}`))

	// status.parents has non-map elements
	f.Add([]byte(`{"status":{"parents":["string",42,null,true]}}`))

	// status.parents[*].conditions is not a slice
	f.Add([]byte(`{"status":{"parents":[{"conditions":"notaslice"}]}}`))

	// GRPCRoute matches.method is a string instead of a map
	f.Add([]byte(`{"spec":{"rules":[{"matches":[{"method":"not-a-map"}]}]}}`))

	// HTTPRoute matches.path is a string instead of a map
	f.Add([]byte(`{"spec":{"rules":[{"matches":[{"path":"not-a-map"}]}]}}`))

	// HTTPRoute matches.headers elements are not maps
	f.Add([]byte(`{"spec":{"rules":[{"matches":[{"headers":["string",42,null]}]}]}}`))

	// filters array has non-map elements
	f.Add([]byte(`{"spec":{"rules":[{"filters":["string",42,null,true]}]}}`))

	// RequestHeaderModifier modifier fields are not slices
	f.Add([]byte(`{
		"spec": {
			"rules": [{
				"filters": [{
					"type": "RequestHeaderModifier",
					"requestHeaderModifier": {
						"set": "not-a-slice",
						"add": 42,
						"remove": {}
					}
				}]
			}]
		}
	}`))

	// requestRedirect fields with wrong types
	f.Add([]byte(`{
		"spec": {
			"rules": [{
				"filters": [{
					"type": "RequestRedirect",
					"requestRedirect": {
						"scheme": 42,
						"hostname": [],
						"statusCode": "three-oh-one"
					}
				}]
			}]
		}
	}`))

	// urlRewrite path is a string instead of a map
	f.Add([]byte(`{
		"spec": {
			"rules": [{
				"filters": [{
					"type": "URLRewrite",
					"urlRewrite": {"path": "not-a-map"}
				}]
			}]
		}
	}`))

	// requestMirror.backendRef is not a map
	f.Add([]byte(`{
		"spec": {
			"rules": [{
				"filters": [{
					"type": "RequestMirror",
					"requestMirror": {"backendRef": "notamap"}
				}]
			}]
		}
	}`))

	// Null values at every top-level key
	f.Add([]byte(`{"spec":null,"metadata":null,"status":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		u, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}

		_ = normalizeGatewayClass(u)
		_ = normalizeGateway(u)
		_ = normalizeGatewayDetail(u)
		_ = normalizeHTTPRoute(u)
		_ = normalizeHTTPRouteDetail(u)
		_ = normalizeGRPCRouteDetail(u)
		_ = normalizeRoute(u, "TCPRoute")
		_ = normalizeRoute(u, "TLSRoute")
		_ = normalizeRoute(u, "UDPRoute")
		_ = normalizeSimpleRouteDetail(u, "TCPRoute")
		_ = normalizeSimpleRouteDetail(u, "TLSRoute")
		_ = normalizeSimpleRouteDetail(u, "UDPRoute")
	})
}
