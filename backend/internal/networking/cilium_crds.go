package networking

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

// Cilium CRD GroupVersionResources.
var (
	bgpNodeConfigGVR  = schema.GroupVersionResource{Group: "cilium.io", Version: "v2alpha1", Resource: "ciliumbgpnodeconfigs"}
	ciliumNodeGVR     = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnodes"}
	ciliumEndpointGVR = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumendpoints"}
)

// hasCRD checks whether the given GVR is available on the API server.
func hasCRD(disc discovery.DiscoveryInterface, gvr schema.GroupVersionResource) bool {
	if disc == nil {
		return false
	}
	resources, err := disc.ServerResourcesForGroupVersion(fmt.Sprintf("%s/%s", gvr.Group, gvr.Version))
	if err != nil {
		return false
	}
	for _, r := range resources.APIResources {
		if r.Name == gvr.Resource {
			return true
		}
	}
	return false
}

// readBGPNodeConfigs reads CiliumBGPNodeConfig CRDs to extract live per-peer session status.
func readBGPNodeConfigs(ctx context.Context, client dynamic.Interface) ([]BGPPeerStatus, error) {
	list, err := client.Resource(bgpNodeConfigGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list CiliumBGPNodeConfig: %w", err)
	}

	var peers []BGPPeerStatus
	for _, obj := range list.Items {
		nodeName := obj.GetName()

		status, _, _ := unstructured.NestedMap(obj.Object, "status")
		if status == nil {
			continue
		}

		instances, _, _ := unstructured.NestedSlice(status, "bgpInstances")
		for _, inst := range instances {
			instMap, ok := inst.(map[string]interface{})
			if !ok {
				continue
			}

			localASN, _, _ := unstructured.NestedInt64(instMap, "localASN")

			peerList, _, _ := unstructured.NestedSlice(instMap, "peers")
			for _, p := range peerList {
				peerMap, ok := p.(map[string]interface{})
				if !ok {
					continue
				}

				ps := BGPPeerStatus{
					Node:     nodeName,
					LocalASN: localASN,
				}

				ps.PeerAddress, _, _ = unstructured.NestedString(peerMap, "peerAddress")
				ps.PeerASN, _, _ = unstructured.NestedInt64(peerMap, "peerASN")
				ps.SessionState, _, _ = unstructured.NestedString(peerMap, "peeringState")

				// Route counts from the routeCount array
				routeCounts, _, _ := unstructured.NestedSlice(peerMap, "routeCount")
				for _, rc := range routeCounts {
					rcMap, ok := rc.(map[string]interface{})
					if !ok {
						continue
					}
					received, _, _ := unstructured.NestedInt64(rcMap, "received")
					advertised, _, _ := unstructured.NestedInt64(rcMap, "advertised")
					ps.RoutesReceived += int(received)
					ps.RoutesAdvertised += int(advertised)
				}

				peers = append(peers, ps)
			}
		}
	}

	sort.Slice(peers, func(i, j int) bool {
		if peers[i].Node != peers[j].Node {
			return peers[i].Node < peers[j].Node
		}
		return peers[i].PeerAddress < peers[j].PeerAddress
	})

	return peers, nil
}

// ciliumNodeIPAM represents raw IPAM data extracted from a CiliumNode CRD.
type ciliumNodeIPAM struct {
	Name          string
	PodCIDRs      []string
	PoolCount     int // number of IPs in spec.ipam.pool (available/pre-allocated)
	UsedCount     int // number of IPs in status.ipam.used (allocated)
	EncryptionKey int64
}

// readCiliumNodes reads CiliumNode CRDs and returns IPAM + encryption data.
func readCiliumNodes(ctx context.Context, client dynamic.Interface) ([]ciliumNodeIPAM, error) {
	list, err := client.Resource(ciliumNodeGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list CiliumNode: %w", err)
	}

	var nodes []ciliumNodeIPAM
	for _, obj := range list.Items {
		n := ciliumNodeIPAM{Name: obj.GetName()}

		// spec.ipam.podCIDRs
		cidrs, _, _ := unstructured.NestedStringSlice(obj.Object, "spec", "ipam", "podCIDRs")
		n.PodCIDRs = cidrs

		// spec.ipam.pool — map of IP → owner string (available/pre-allocated IPs)
		pool, _, _ := unstructured.NestedMap(obj.Object, "spec", "ipam", "pool")
		n.PoolCount = len(pool)

		// status.ipam.used — map of IP → owner string (currently allocated IPs)
		used, _, _ := unstructured.NestedMap(obj.Object, "status", "ipam", "used")
		n.UsedCount = len(used)

		// spec.encryption.key — non-zero means encryption is active on this node
		n.EncryptionKey, _, _ = unstructured.NestedInt64(obj.Object, "spec", "encryption", "key")

		nodes = append(nodes, n)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})

	return nodes, nil
}

// aggregateEndpoints reads CiliumEndpoint CRDs and returns aggregate state counts.
// Uses pagination (500 per page) to avoid unbounded list responses in large clusters.
func aggregateEndpoints(ctx context.Context, client dynamic.Interface) (EndpointCounts, error) {
	var counts EndpointCounts
	opts := metav1.ListOptions{Limit: 500}
	for {
		list, err := client.Resource(ciliumEndpointGVR).Namespace("").List(ctx, opts)
		if err != nil {
			return EndpointCounts{}, fmt.Errorf("list CiliumEndpoint: %w", err)
		}

		for _, obj := range list.Items {
			counts.Total++

			state, _, _ := unstructured.NestedString(obj.Object, "status", "state")
			switch state {
			case "ready":
				counts.Ready++
			case "not-ready":
				counts.NotReady++
			case "disconnecting", "disconnected":
				counts.Disconnecting++
			case "waiting-for-identity", "waiting-to-regenerate", "regenerating", "restoring":
				counts.Waiting++
			default:
				// Unknown states count as not-ready
				counts.NotReady++
			}
		}

		if list.GetContinue() == "" {
			break
		}
		opts.Continue = list.GetContinue()
	}

	return counts, nil
}
