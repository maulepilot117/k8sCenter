// Package servicemesh provides Istio and Linkerd service mesh observability.
//
// Phase A surfaces mesh installation status, traffic-routing CRDs (VirtualService,
// DestinationRule, ServiceProfile, etc.), and RBAC-filtered list/detail views.
// Phase B adds mTLS posture and golden-signal metrics. Phase C builds the UI.
// Phase D overlays mesh edges on the existing topology graph.
//
// The package mirrors internal/gitops: per-mesh adapter files keep mesh-specific
// logic isolated, while shared normalized types provide a stable UI contract.
package servicemesh

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// MeshType identifies which service mesh manages a resource.
type MeshType string

const (
	MeshNone    MeshType = ""
	MeshIstio   MeshType = "istio"
	MeshLinkerd MeshType = "linkerd"
	MeshBoth    MeshType = "both"
)

// MeshMode distinguishes Istio's sidecar and ambient data-plane modes.
// Linkerd has no equivalent toggle today.
type MeshMode string

const (
	MeshModeSidecar MeshMode = "sidecar"
	MeshModeAmbient MeshMode = "ambient"
)

// Istio CRD GroupVersionResources (networking.istio.io/v1, security.istio.io/v1).
var (
	IstioVirtualServiceGVR = schema.GroupVersionResource{
		Group: "networking.istio.io", Version: "v1", Resource: "virtualservices",
	}
	IstioDestinationRuleGVR = schema.GroupVersionResource{
		Group: "networking.istio.io", Version: "v1", Resource: "destinationrules",
	}
	IstioGatewayGVR = schema.GroupVersionResource{
		Group: "networking.istio.io", Version: "v1", Resource: "gateways",
	}
	IstioServiceEntryGVR = schema.GroupVersionResource{
		Group: "networking.istio.io", Version: "v1", Resource: "serviceentries",
	}
	IstioPeerAuthenticationGVR = schema.GroupVersionResource{
		Group: "security.istio.io", Version: "v1", Resource: "peerauthentications",
	}
	IstioAuthorizationPolicyGVR = schema.GroupVersionResource{
		Group: "security.istio.io", Version: "v1", Resource: "authorizationpolicies",
	}
)

// Linkerd CRD GroupVersionResources.
// Note: Linkerd's HTTPRoute lives under policy.linkerd.io and is distinct
// from the upstream gateway.networking.k8s.io HTTPRoute. The handler disambiguates
// by API group.
var (
	LinkerdServiceProfileGVR = schema.GroupVersionResource{
		Group: "linkerd.io", Version: "v1beta1", Resource: "serviceprofiles",
	}
	LinkerdServerGVR = schema.GroupVersionResource{
		Group: "policy.linkerd.io", Version: "v1beta3", Resource: "servers",
	}
	LinkerdAuthorizationPolicyGVR = schema.GroupVersionResource{
		Group: "policy.linkerd.io", Version: "v1alpha1", Resource: "authorizationpolicies",
	}
	LinkerdHTTPRouteGVR = schema.GroupVersionResource{
		Group: "policy.linkerd.io", Version: "v1beta1", Resource: "httproutes",
	}
	LinkerdMeshTLSAuthenticationGVR = schema.GroupVersionResource{
		Group: "policy.linkerd.io", Version: "v1alpha1", Resource: "meshtlsauthentications",
	}
)

// MeshInfo describes a single mesh's installation state.
type MeshInfo struct {
	Installed bool     `json:"installed"`
	Namespace string   `json:"namespace,omitempty"`
	Version   string   `json:"version,omitempty"`
	Mode      MeshMode `json:"mode,omitempty"` // Istio only; empty for Linkerd
}

// MeshStatus is returned by GET /mesh/status.
//
// Callers must treat the returned value as read-only: mutating the Istio
// or Linkerd pointer fields may race with concurrent cache updates.
type MeshStatus struct {
	Detected    MeshType  `json:"detected"`
	Istio       *MeshInfo `json:"istio,omitempty"`
	Linkerd     *MeshInfo `json:"linkerd,omitempty"`
	LastChecked time.Time `json:"lastChecked"`
}

// RouteDestination identifies one backend a TrafficRoute forwards to.
// Fields are optional: meshes differ in what they expose (e.g., Linkerd
// ServiceProfiles don't carry port or subset).
type RouteDestination struct {
	Host   string `json:"host,omitempty"`
	Subset string `json:"subset,omitempty"`
	Port   int64  `json:"port,omitempty"`
	Weight int64  `json:"weight,omitempty"`
}

// TrafficRoute is the mesh-agnostic shape for routing CRDs (Istio
// VirtualService / DestinationRule / Gateway, Linkerd ServiceProfile /
// HTTPRoute / Server). `Mesh` + `Kind` discriminate for typed UI handling.
// `Raw` preserves the full spec for detail-view rendering and YAML export.
type TrafficRoute struct {
	ID           string             `json:"id"` // composite: "{mesh}:{namespace}:{kindCode}:{name}"
	Mesh         MeshType           `json:"mesh"`
	Kind         string             `json:"kind"`
	Name         string             `json:"name"`
	Namespace    string             `json:"namespace,omitempty"`
	Hosts        []string           `json:"hosts,omitempty"`
	Gateways     []string           `json:"gateways,omitempty"`
	Subsets      []string           `json:"subsets,omitempty"`
	Destinations []RouteDestination `json:"destinations,omitempty"`
	Raw          map[string]any     `json:"raw,omitempty"`
}

// MeshedPolicy is the mesh-agnostic shape for security / authz CRDs
// (Istio PeerAuthentication / AuthorizationPolicy, Linkerd MeshTLSAuthentication
// / AuthorizationPolicy). `Effect` captures computed semantics for corner
// cases (e.g., ALLOW with no rules → "deny_all").
type MeshedPolicy struct {
	ID        string         `json:"id"`
	Mesh      MeshType       `json:"mesh"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	Namespace string         `json:"namespace,omitempty"`
	Action    string         `json:"action,omitempty"`   // ALLOW / DENY / AUDIT / CUSTOM / ... (mesh-native)
	Effect    string         `json:"effect,omitempty"`   // computed: "deny_all" | "allow_all" | ""
	MTLSMode  string         `json:"mtlsMode,omitempty"` // PeerAuthentication: STRICT / PERMISSIVE / DISABLE / UNSET
	Selector  string         `json:"selector,omitempty"` // stringified matchLabels: "k=v,k=v"
	RuleCount int            `json:"ruleCount"`
	Raw       map[string]any `json:"raw,omitempty"`
}

// NormalizedMeshMembership reports per-namespace mesh injection status —
// the signal that drives the coverage summary on the mesh dashboard and
// the mTLS posture page. Populated in detail in Phase B; the type is
// surfaced here so downstream packages can already reference it.
type NormalizedMeshMembership struct {
	Namespace      string   `json:"namespace"`
	Mesh           MeshType `json:"mesh,omitempty"` // empty = unmeshed
	InjectionLabel string   `json:"injectionLabel,omitempty"`
}
