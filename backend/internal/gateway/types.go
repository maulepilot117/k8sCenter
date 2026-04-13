// Package gateway provides Gateway API integration for k8sCenter.
package gateway

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GVR constants for gateway.networking.k8s.io resources.
var (
	GatewayClassGVR = schema.GroupVersionResource{
		Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses",
	}
	GatewayGVR = schema.GroupVersionResource{
		Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways",
	}
	HTTPRouteGVR = schema.GroupVersionResource{
		Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes",
	}
	GRPCRouteGVR = schema.GroupVersionResource{
		Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes",
	}
	TCPRouteGVR = schema.GroupVersionResource{
		Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tcproutes",
	}
	TLSRouteGVR = schema.GroupVersionResource{
		Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tlsroutes",
	}
	UDPRouteGVR = schema.GroupVersionResource{
		Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "udproutes",
	}
)

// APIGroup is the Gateway API group name.
const APIGroup = "gateway.networking.k8s.io"

// cacheTTL is the duration for which cached Gateway API data is considered fresh.
const cacheTTL = 30 * time.Second

// routeKind identifies a non-HTTP route resource type for dispatch.
type routeKind string

const (
	RouteKindGRPC routeKind = "grpcroutes"
	RouteKindTCP  routeKind = "tcproutes"
	RouteKindTLS  routeKind = "tlsroutes"
	RouteKindUDP  routeKind = "udproutes"
)

// routeKindGVR maps each routeKind to its GroupVersionResource.
var routeKindGVR = map[routeKind]schema.GroupVersionResource{
	RouteKindGRPC: GRPCRouteGVR,
	RouteKindTCP:  TCPRouteGVR,
	RouteKindTLS:  TLSRouteGVR,
	RouteKindUDP:  UDPRouteGVR,
}

// GatewayAPIStatus is returned by GET /gateway/status.
type GatewayAPIStatus struct {
	Available      bool      `json:"available"`
	Version        string    `json:"version,omitempty"`
	InstalledKinds []string  `json:"installedKinds,omitempty"`
	LastChecked    time.Time `json:"lastChecked"`
}

// GatewayAPISummary aggregates counts across all Gateway API resource kinds.
type GatewayAPISummary struct {
	GatewayClasses KindSummary `json:"gatewayClasses"`
	Gateways       KindSummary `json:"gateways"`
	HTTPRoutes     KindSummary `json:"httpRoutes"`
	GRPCRoutes     KindSummary `json:"grpcRoutes"`
	TCPRoutes      KindSummary `json:"tcpRoutes"`
	TLSRoutes      KindSummary `json:"tlsRoutes"`
	UDPRoutes      KindSummary `json:"udpRoutes"`
}

// KindSummary provides health-categorized counts for a single resource kind.
type KindSummary struct {
	Total    int `json:"total"`
	Healthy  int `json:"healthy"`
	Degraded int `json:"degraded"`
}

// Condition represents a Kubernetes-style status condition.
type Condition struct {
	Type               string     `json:"type"`
	Status             string     `json:"status"`
	Reason             string     `json:"reason"`
	Message            string     `json:"message"`
	LastTransitionTime *time.Time `json:"lastTransitionTime,omitempty"`
}

// ParentRef identifies a parent resource (typically a Gateway) that a route is attached to.
type ParentRef struct {
	Group             string      `json:"group"`
	Kind              string      `json:"kind"`
	Name              string      `json:"name"`
	Namespace         string      `json:"namespace"`
	SectionName       string      `json:"sectionName"`
	Status            string      `json:"status"`
	GatewayConditions []Condition `json:"gatewayConditions,omitempty"`
}

// BackendRef identifies a backend target for route traffic.
type BackendRef struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Port      *int   `json:"port,omitempty"`
	Weight    *int   `json:"weight,omitempty"`
	Resolved  bool   `json:"resolved"`
}

// RouteSummary is a generic route representation used for non-HTTP route listings.
type RouteSummary struct {
	Kind       string      `json:"kind"`
	Name       string      `json:"name"`
	Namespace  string      `json:"namespace"`
	Hostnames  []string    `json:"hostnames,omitempty"`
	ParentRefs []ParentRef `json:"parentRefs,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
	Age        time.Time   `json:"age"`
}

// GatewayClassSummary is the API representation of a GatewayClass resource.
type GatewayClassSummary struct {
	Name           string      `json:"name"`
	ControllerName string     `json:"controllerName"`
	Description    string      `json:"description,omitempty"`
	Conditions     []Condition `json:"conditions,omitempty"`
	Age            time.Time   `json:"age"`
}

// Listener represents a single listener on a Gateway.
type Listener struct {
	Name               string      `json:"name"`
	Port               int         `json:"port"`
	Protocol           string      `json:"protocol"`
	Hostname           string      `json:"hostname,omitempty"`
	AttachedRouteCount int         `json:"attachedRouteCount"`
	TLSMode            string      `json:"tlsMode,omitempty"`
	CertificateRef     string      `json:"certificateRef,omitempty"`
	AllowedRoutes      string      `json:"allowedRoutes,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
}

// GatewaySummary is the API representation of a Gateway resource.
type GatewaySummary struct {
	Name               string      `json:"name"`
	Namespace          string      `json:"namespace"`
	GatewayClassName   string      `json:"gatewayClassName"`
	Listeners          []Listener  `json:"listeners"`
	Addresses          []string    `json:"addresses,omitempty"`
	AttachedRouteCount int         `json:"attachedRouteCount"`
	Conditions         []Condition `json:"conditions,omitempty"`
	Age                time.Time   `json:"age"`
}

// GatewayDetail extends GatewaySummary with attached route information.
type GatewayDetail struct {
	GatewaySummary
	AttachedRoutes []RouteSummary `json:"attachedRoutes"`
}

// HTTPRouteSummary is the API representation of an HTTPRoute resource.
type HTTPRouteSummary struct {
	Name         string      `json:"name"`
	Namespace    string      `json:"namespace"`
	Hostnames    []string    `json:"hostnames,omitempty"`
	ParentRefs   []ParentRef `json:"parentRefs,omitempty"`
	BackendCount int         `json:"backendCount"`
	Conditions   []Condition `json:"conditions,omitempty"`
	Age          time.Time   `json:"age"`
}

// HTTPRouteMatch describes a single match clause in an HTTPRoute rule.
type HTTPRouteMatch struct {
	PathType    string   `json:"pathType"`
	PathValue   string   `json:"pathValue"`
	Headers     []string `json:"headers,omitempty"`
	Method      string   `json:"method,omitempty"`
	QueryParams []string `json:"queryParams,omitempty"`
}

// HTTPRouteFilter describes a filter applied to matched HTTP traffic.
type HTTPRouteFilter struct {
	Type    string `json:"type"`
	Details string `json:"details"`
}

// HTTPRouteRule represents a single rule in an HTTPRoute spec.
type HTTPRouteRule struct {
	Matches     []HTTPRouteMatch  `json:"matches,omitempty"`
	Filters     []HTTPRouteFilter `json:"filters,omitempty"`
	BackendRefs []BackendRef      `json:"backendRefs,omitempty"`
}

// HTTPRouteDetail is the full detail view of an HTTPRoute resource.
type HTTPRouteDetail struct {
	Name         string          `json:"name"`
	Namespace    string          `json:"namespace"`
	Hostnames    []string        `json:"hostnames,omitempty"`
	ParentRefs   []ParentRef     `json:"parentRefs,omitempty"`
	BackendCount int             `json:"backendCount"`
	Conditions   []Condition     `json:"conditions,omitempty"`
	Age          time.Time       `json:"age"`
	Rules        []HTTPRouteRule `json:"rules,omitempty"`
}

// GRPCRouteMatch describes a single match clause in a GRPCRoute rule.
type GRPCRouteMatch struct {
	Service string   `json:"service"`
	Method  string   `json:"method"`
	Headers []string `json:"headers,omitempty"`
}

// GRPCRouteRule represents a single rule in a GRPCRoute spec.
type GRPCRouteRule struct {
	Matches     []GRPCRouteMatch `json:"matches,omitempty"`
	BackendRefs []BackendRef     `json:"backendRefs,omitempty"`
}

// GRPCRouteDetail is the full detail view of a GRPCRoute resource.
type GRPCRouteDetail struct {
	Name       string          `json:"name"`
	Namespace  string          `json:"namespace"`
	ParentRefs []ParentRef     `json:"parentRefs,omitempty"`
	Rules      []GRPCRouteRule `json:"rules,omitempty"`
	Conditions []Condition     `json:"conditions,omitempty"`
	Age        time.Time       `json:"age"`
}

// SimpleRouteDetail is the detail view for TCP, TLS, and UDP routes.
type SimpleRouteDetail struct {
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	Namespace   string       `json:"namespace"`
	Hostnames   []string     `json:"hostnames,omitempty"`
	ParentRefs  []ParentRef  `json:"parentRefs,omitempty"`
	BackendRefs []BackendRef `json:"backendRefs,omitempty"`
	Conditions  []Condition  `json:"conditions,omitempty"`
	Age         time.Time    `json:"age"`
}

// namespacedResource is implemented by types that belong to a Kubernetes namespace.
type namespacedResource interface {
	getNamespace() string
}

func (g GatewaySummary) getNamespace() string  { return g.Namespace }
func (h HTTPRouteSummary) getNamespace() string { return h.Namespace }
func (r RouteSummary) getNamespace() string     { return r.Namespace }
