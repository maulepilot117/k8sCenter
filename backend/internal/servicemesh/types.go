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
	Detected    MeshType   `json:"detected"`
	Istio       *MeshInfo  `json:"istio,omitempty"`
	Linkerd     *MeshInfo  `json:"linkerd,omitempty"`
	LastChecked time.Time  `json:"lastChecked"`
}
