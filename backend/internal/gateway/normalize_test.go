package gateway

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func makeUnstructured(obj map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: obj}
}

func TestNormalizeGatewayClass(t *testing.T) {
	tests := []struct {
		name           string
		obj            map[string]any
		wantName       string
		wantController string
		wantDesc       string
		wantConditions int
	}{
		{
			name: "full gateway class",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "cilium",
					"creationTimestamp": "2024-01-01T00:00:00Z",
				},
				"spec": map[string]any{
					"controllerName": "io.cilium/gateway-controller",
					"description":    "Cilium GatewayClass",
				},
				"status": map[string]any{
					"conditions": []any{
						map[string]any{
							"type":   "Accepted",
							"status": "True",
							"reason": "Accepted",
						},
					},
				},
			},
			wantName:       "cilium",
			wantController: "io.cilium/gateway-controller",
			wantDesc:       "Cilium GatewayClass",
			wantConditions: 1,
		},
		{
			name: "minimal gateway class",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "minimal",
					"creationTimestamp": "2025-06-15T12:00:00Z",
				},
				"spec": map[string]any{},
			},
			wantName:       "minimal",
			wantController: "",
			wantDesc:       "",
			wantConditions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := makeUnstructured(tt.obj)
			got := normalizeGatewayClass(u)

			if got.Name != tt.wantName {
				t.Errorf("Name = %q; want %q", got.Name, tt.wantName)
			}
			if got.ControllerName != tt.wantController {
				t.Errorf("ControllerName = %q; want %q", got.ControllerName, tt.wantController)
			}
			if got.Description != tt.wantDesc {
				t.Errorf("Description = %q; want %q", got.Description, tt.wantDesc)
			}
			if len(got.Conditions) != tt.wantConditions {
				t.Fatalf("Conditions len = %d; want %d", len(got.Conditions), tt.wantConditions)
			}
			if tt.wantConditions > 0 {
				if got.Conditions[0].Type != "Accepted" {
					t.Errorf("Conditions[0].Type = %q; want %q", got.Conditions[0].Type, "Accepted")
				}
				if got.Conditions[0].Status != "True" {
					t.Errorf("Conditions[0].Status = %q; want %q", got.Conditions[0].Status, "True")
				}
			}
			if got.Age.IsZero() {
				t.Error("Age is zero; want non-zero")
			}
		})
	}
}

func TestNormalizeGateway(t *testing.T) {
	tests := []struct {
		name               string
		obj                map[string]any
		wantName           string
		wantNamespace      string
		wantClassName      string
		wantListeners      int
		wantAddresses      int
		wantAttachedRoutes int
		wantConditions     int
	}{
		{
			name: "gateway with listeners and addresses",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "my-gateway",
					"namespace":         "default",
					"creationTimestamp": "2024-06-01T10:00:00Z",
				},
				"spec": map[string]any{
					"gatewayClassName": "cilium",
					"listeners": []any{
						map[string]any{
							"name":     "http",
							"port":     int64(80),
							"protocol": "HTTP",
							"hostname": "example.com",
						},
						map[string]any{
							"name":     "https",
							"port":     int64(443),
							"protocol": "HTTPS",
							"hostname": "example.com",
						},
					},
				},
				"status": map[string]any{
					"addresses": []any{
						map[string]any{"value": "10.0.0.1"},
						map[string]any{"value": "10.0.0.2"},
					},
					"conditions": []any{
						map[string]any{
							"type":   "Accepted",
							"status": "True",
							"reason": "Accepted",
						},
					},
					"listeners": []any{
						map[string]any{
							"name":           "http",
							"attachedRoutes": int64(3),
						},
						map[string]any{
							"name":           "https",
							"attachedRoutes": int64(2),
						},
					},
				},
			},
			wantName:           "my-gateway",
			wantNamespace:      "default",
			wantClassName:      "cilium",
			wantListeners:      2,
			wantAddresses:      2,
			wantAttachedRoutes: 5,
			wantConditions:     1,
		},
		{
			name: "gateway with zero listeners",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "empty-gw",
					"namespace":         "infra",
					"creationTimestamp": "2025-01-01T00:00:00Z",
				},
				"spec": map[string]any{
					"gatewayClassName": "istio",
				},
			},
			wantName:           "empty-gw",
			wantNamespace:      "infra",
			wantClassName:      "istio",
			wantListeners:      0,
			wantAddresses:      0,
			wantAttachedRoutes: 0,
			wantConditions:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := makeUnstructured(tt.obj)
			got := normalizeGateway(u)

			if got.Name != tt.wantName {
				t.Errorf("Name = %q; want %q", got.Name, tt.wantName)
			}
			if got.Namespace != tt.wantNamespace {
				t.Errorf("Namespace = %q; want %q", got.Namespace, tt.wantNamespace)
			}
			if got.GatewayClassName != tt.wantClassName {
				t.Errorf("GatewayClassName = %q; want %q", got.GatewayClassName, tt.wantClassName)
			}
			if len(got.Listeners) != tt.wantListeners {
				t.Fatalf("Listeners len = %d; want %d", len(got.Listeners), tt.wantListeners)
			}
			if len(got.Addresses) != tt.wantAddresses {
				t.Errorf("Addresses len = %d; want %d", len(got.Addresses), tt.wantAddresses)
			}
			if got.AttachedRouteCount != tt.wantAttachedRoutes {
				t.Errorf("AttachedRouteCount = %d; want %d", got.AttachedRouteCount, tt.wantAttachedRoutes)
			}
			if len(got.Conditions) != tt.wantConditions {
				t.Errorf("Conditions len = %d; want %d", len(got.Conditions), tt.wantConditions)
			}
			// Verify per-listener attached route counts are merged from status.
			if tt.wantListeners == 2 {
				if got.Listeners[0].AttachedRouteCount != 3 {
					t.Errorf("Listeners[0].AttachedRouteCount = %d; want 3", got.Listeners[0].AttachedRouteCount)
				}
				if got.Listeners[1].AttachedRouteCount != 2 {
					t.Errorf("Listeners[1].AttachedRouteCount = %d; want 2", got.Listeners[1].AttachedRouteCount)
				}
			}
		})
	}
}

func TestNormalizeHTTPRoute(t *testing.T) {
	tests := []struct {
		name             string
		obj              map[string]any
		wantName         string
		wantNamespace    string
		wantHostnames    int
		wantParentRefs   int
		wantBackendCount int
	}{
		{
			name: "httproute with hostnames and backends",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "web-route",
					"namespace":         "apps",
					"creationTimestamp": "2024-03-01T08:00:00Z",
				},
				"spec": map[string]any{
					"hostnames": []any{"app.example.com", "www.example.com"},
					"parentRefs": []any{
						map[string]any{
							"name":      "my-gateway",
							"namespace": "default",
						},
					},
					"rules": []any{
						map[string]any{
							"backendRefs": []any{
								map[string]any{"name": "svc-a", "port": int64(8080)},
								map[string]any{"name": "svc-b", "port": int64(8080)},
							},
						},
						map[string]any{
							"backendRefs": []any{
								map[string]any{"name": "svc-c", "port": int64(9090)},
							},
						},
					},
				},
			},
			wantName:         "web-route",
			wantNamespace:    "apps",
			wantHostnames:    2,
			wantParentRefs:   1,
			wantBackendCount: 3,
		},
		{
			name: "httproute with empty rules",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "empty-route",
					"namespace":         "test",
					"creationTimestamp": "2025-01-01T00:00:00Z",
				},
				"spec": map[string]any{},
			},
			wantName:         "empty-route",
			wantNamespace:    "test",
			wantHostnames:    0,
			wantParentRefs:   0,
			wantBackendCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := makeUnstructured(tt.obj)
			got := normalizeHTTPRoute(u)

			if got.Name != tt.wantName {
				t.Errorf("Name = %q; want %q", got.Name, tt.wantName)
			}
			if got.Namespace != tt.wantNamespace {
				t.Errorf("Namespace = %q; want %q", got.Namespace, tt.wantNamespace)
			}
			if len(got.Hostnames) != tt.wantHostnames {
				t.Errorf("Hostnames len = %d; want %d", len(got.Hostnames), tt.wantHostnames)
			}
			if len(got.ParentRefs) != tt.wantParentRefs {
				t.Errorf("ParentRefs len = %d; want %d", len(got.ParentRefs), tt.wantParentRefs)
			}
			if got.BackendCount != tt.wantBackendCount {
				t.Errorf("BackendCount = %d; want %d", got.BackendCount, tt.wantBackendCount)
			}
		})
	}
}

func TestNormalizeRoute(t *testing.T) {
	tests := []struct {
		name          string
		kind          string
		obj           map[string]any
		wantKind      string
		wantName      string
		wantHostnames int
	}{
		{
			name: "TCPRoute",
			kind: "TCPRoute",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "tcp-route",
					"namespace":         "infra",
					"creationTimestamp": "2024-05-01T00:00:00Z",
				},
				"spec": map[string]any{},
			},
			wantKind:      "TCPRoute",
			wantName:      "tcp-route",
			wantHostnames: 0,
		},
		{
			name: "TLSRoute with hostnames",
			kind: "TLSRoute",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "tls-route",
					"namespace":         "secure",
					"creationTimestamp": "2024-05-01T00:00:00Z",
				},
				"spec": map[string]any{
					"hostnames": []any{"secure.example.com"},
				},
			},
			wantKind:      "TLSRoute",
			wantName:      "tls-route",
			wantHostnames: 1,
		},
		{
			name: "UDPRoute",
			kind: "UDPRoute",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "udp-route",
					"namespace":         "gaming",
					"creationTimestamp": "2024-05-01T00:00:00Z",
				},
				"spec": map[string]any{},
			},
			wantKind:      "UDPRoute",
			wantName:      "udp-route",
			wantHostnames: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := makeUnstructured(tt.obj)
			got := normalizeRoute(u, tt.kind)

			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q; want %q", got.Kind, tt.wantKind)
			}
			if got.Name != tt.wantName {
				t.Errorf("Name = %q; want %q", got.Name, tt.wantName)
			}
			if len(got.Hostnames) != tt.wantHostnames {
				t.Errorf("Hostnames len = %d; want %d", len(got.Hostnames), tt.wantHostnames)
			}
		})
	}
}

func TestNormalizeSimpleRouteDetail(t *testing.T) {
	tests := []struct {
		name            string
		kind            string
		obj             map[string]any
		wantKind        string
		wantHostnames   int
		wantBackendRefs int
	}{
		{
			name: "TLSRoute with hostnames and backends",
			kind: "TLSRoute",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "tls-detail",
					"namespace":         "secure",
					"creationTimestamp": "2024-07-01T00:00:00Z",
				},
				"spec": map[string]any{
					"hostnames": []any{"secure.example.com", "api.example.com"},
					"parentRefs": []any{
						map[string]any{"name": "my-gw", "namespace": "default"},
					},
					"rules": []any{
						map[string]any{
							"backendRefs": []any{
								map[string]any{"name": "backend-svc", "port": int64(443)},
								map[string]any{"name": "fallback-svc", "port": int64(443)},
							},
						},
					},
				},
			},
			wantKind:        "TLSRoute",
			wantHostnames:   2,
			wantBackendRefs: 2,
		},
		{
			name: "TCPRoute with no hostnames",
			kind: "TCPRoute",
			obj: map[string]any{
				"metadata": map[string]any{
					"name":              "tcp-detail",
					"namespace":         "infra",
					"creationTimestamp": "2024-07-01T00:00:00Z",
				},
				"spec": map[string]any{},
			},
			wantKind:        "TCPRoute",
			wantHostnames:   0,
			wantBackendRefs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := makeUnstructured(tt.obj)
			got := normalizeSimpleRouteDetail(u, tt.kind)

			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q; want %q", got.Kind, tt.wantKind)
			}
			if len(got.Hostnames) != tt.wantHostnames {
				t.Errorf("Hostnames len = %d; want %d", len(got.Hostnames), tt.wantHostnames)
			}
			if len(got.BackendRefs) != tt.wantBackendRefs {
				t.Fatalf("BackendRefs len = %d; want %d", len(got.BackendRefs), tt.wantBackendRefs)
			}
			if tt.wantBackendRefs > 0 {
				if got.BackendRefs[0].Name != "backend-svc" {
					t.Errorf("BackendRefs[0].Name = %q; want %q", got.BackendRefs[0].Name, "backend-svc")
				}
			}
		})
	}
}

func TestExtractParentRefs(t *testing.T) {
	tests := []struct {
		name      string
		obj       map[string]any
		wantLen   int
		wantGroup string
		wantKind  string
	}{
		{
			name: "explicit group and kind",
			obj: map[string]any{
				"spec": map[string]any{
					"parentRefs": []any{
						map[string]any{
							"group":       "custom.io",
							"kind":        "CustomGateway",
							"name":        "my-gw",
							"namespace":   "infra",
							"sectionName": "https",
						},
					},
				},
			},
			wantLen:   1,
			wantGroup: "custom.io",
			wantKind:  "CustomGateway",
		},
		{
			name: "default group and kind",
			obj: map[string]any{
				"spec": map[string]any{
					"parentRefs": []any{
						map[string]any{
							"name":      "default-gw",
							"namespace": "default",
						},
					},
				},
			},
			wantLen:   1,
			wantGroup: "gateway.networking.k8s.io",
			wantKind:  "Gateway",
		},
		{
			name: "empty parentRefs",
			obj: map[string]any{
				"spec": map[string]any{},
			},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := makeUnstructured(tt.obj)
			got := extractParentRefs(u)

			if len(got) != tt.wantLen {
				t.Fatalf("len = %d; want %d", len(got), tt.wantLen)
			}
			if tt.wantLen > 0 {
				if got[0].Group != tt.wantGroup {
					t.Errorf("Group = %q; want %q", got[0].Group, tt.wantGroup)
				}
				if got[0].Kind != tt.wantKind {
					t.Errorf("Kind = %q; want %q", got[0].Kind, tt.wantKind)
				}
			}
		})
	}
}

func TestExtractBackendRefs(t *testing.T) {
	tests := []struct {
		name       string
		items      []any
		wantLen    int
		wantKind   string
		wantPort   *int
		wantWeight *int
	}{
		{
			name: "service with port and weight",
			items: []any{
				map[string]any{
					"kind":   "Service",
					"name":   "backend",
					"port":   int64(8080),
					"weight": int64(75),
				},
			},
			wantLen:    1,
			wantKind:   "Service",
			wantPort:   intPtr(8080),
			wantWeight: intPtr(75),
		},
		{
			name: "default kind when omitted",
			items: []any{
				map[string]any{
					"name": "implicit-svc",
					"port": int64(3000),
				},
			},
			wantLen:  1,
			wantKind: "Service",
			wantPort: intPtr(3000),
		},
		{
			name: "missing port",
			items: []any{
				map[string]any{
					"name": "no-port-svc",
				},
			},
			wantLen:  1,
			wantKind: "Service",
			wantPort: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBackendRefs(tt.items)

			if len(got) != tt.wantLen {
				t.Fatalf("len = %d; want %d", len(got), tt.wantLen)
			}
			if tt.wantLen > 0 {
				if got[0].Kind != tt.wantKind {
					t.Errorf("Kind = %q; want %q", got[0].Kind, tt.wantKind)
				}
				if tt.wantPort == nil {
					if got[0].Port != nil {
						t.Errorf("Port = %v; want nil", *got[0].Port)
					}
				} else {
					if got[0].Port == nil {
						t.Fatalf("Port is nil; want %d", *tt.wantPort)
					}
					if *got[0].Port != *tt.wantPort {
						t.Errorf("Port = %d; want %d", *got[0].Port, *tt.wantPort)
					}
				}
				if tt.wantWeight == nil {
					if got[0].Weight != nil {
						t.Errorf("Weight = %v; want nil", *got[0].Weight)
					}
				} else {
					if got[0].Weight == nil {
						t.Fatalf("Weight is nil; want %d", *tt.wantWeight)
					}
					if *got[0].Weight != *tt.wantWeight {
						t.Errorf("Weight = %d; want %d", *got[0].Weight, *tt.wantWeight)
					}
				}
			}
		})
	}
}

func TestExtractConditions(t *testing.T) {
	transitionTime := "2024-06-01T12:00:00Z"
	parsedTime, _ := time.Parse(time.RFC3339, transitionTime)

	tests := []struct {
		name    string
		obj     map[string]any
		path    []string
		wantLen int
	}{
		{
			name: "multiple conditions with all fields",
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{
							"type":               "Accepted",
							"status":             "True",
							"reason":             "Accepted",
							"message":            "Gateway accepted",
							"lastTransitionTime": transitionTime,
						},
						map[string]any{
							"type":    "Programmed",
							"status":  "True",
							"reason":  "Programmed",
							"message": "Gateway programmed",
						},
					},
				},
			},
			path:    []string{"status", "conditions"},
			wantLen: 2,
		},
		{
			name: "empty conditions list",
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{},
				},
			},
			path:    []string{"status", "conditions"},
			wantLen: 0,
		},
		{
			name:    "missing conditions path",
			obj:     map[string]any{},
			path:    []string{"status", "conditions"},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractConditions(tt.obj, tt.path...)

			if len(got) != tt.wantLen {
				t.Fatalf("len = %d; want %d", len(got), tt.wantLen)
			}
			if tt.name == "multiple conditions with all fields" {
				if got[0].Type != "Accepted" {
					t.Errorf("Conditions[0].Type = %q; want %q", got[0].Type, "Accepted")
				}
				if got[0].Status != "True" {
					t.Errorf("Conditions[0].Status = %q; want %q", got[0].Status, "True")
				}
				if got[0].Message != "Gateway accepted" {
					t.Errorf("Conditions[0].Message = %q; want %q", got[0].Message, "Gateway accepted")
				}
				if got[0].LastTransitionTime == nil {
					t.Fatal("Conditions[0].LastTransitionTime is nil; want non-nil")
				}
				if !got[0].LastTransitionTime.Equal(parsedTime) {
					t.Errorf("Conditions[0].LastTransitionTime = %v; want %v", got[0].LastTransitionTime, parsedTime)
				}
				if got[1].Type != "Programmed" {
					t.Errorf("Conditions[1].Type = %q; want %q", got[1].Type, "Programmed")
				}
				if got[1].LastTransitionTime != nil {
					t.Errorf("Conditions[1].LastTransitionTime = %v; want nil", got[1].LastTransitionTime)
				}
			}
		})
	}
}

func TestAggregateRouteConditions(t *testing.T) {
	tests := []struct {
		name    string
		obj     map[string]any
		wantLen int
	}{
		{
			name: "two parents with overlapping conditions",
			obj: map[string]any{
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{
									"type":   "Accepted",
									"status": "True",
									"reason": "Accepted",
								},
								map[string]any{
									"type":   "ResolvedRefs",
									"status": "True",
									"reason": "ResolvedRefs",
								},
							},
						},
						map[string]any{
							"conditions": []any{
								map[string]any{
									"type":   "Accepted",
									"status": "True",
									"reason": "Accepted",
								},
								map[string]any{
									"type":   "Programmed",
									"status": "True",
									"reason": "Programmed",
								},
							},
						},
					},
				},
			},
			wantLen: 3, // Accepted (deduped), ResolvedRefs, Programmed
		},
		{
			name: "no parents",
			obj: map[string]any{
				"status": map[string]any{},
			},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateRouteConditions(tt.obj)

			if len(got) != tt.wantLen {
				t.Fatalf("len = %d; want %d", len(got), tt.wantLen)
			}
			if tt.wantLen == 3 {
				// Verify deduplication: should have Accepted, ResolvedRefs, Programmed.
				types := map[string]bool{}
				for _, c := range got {
					types[c.Type] = true
				}
				for _, expected := range []string{"Accepted", "ResolvedRefs", "Programmed"} {
					if !types[expected] {
						t.Errorf("missing expected condition type %q", expected)
					}
				}
			}
		})
	}
}

// intPtr is a helper that returns a pointer to an int value.
func intPtr(v int) *int { return &v }
