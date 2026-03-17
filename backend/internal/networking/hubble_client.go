package networking

import (
	"context"
	"fmt"
	"io"
	"time"

	flowpb "github.com/kubecenter/kubecenter/internal/networking/hubbleproto/flow"
	observerpb "github.com/kubecenter/kubecenter/internal/networking/hubbleproto/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// FlowRecord is the JSON-serializable representation of a Hubble flow.
// Only includes fields needed for the frontend flow table.
type FlowRecord struct {
	Time         time.Time `json:"time"`
	Verdict      string    `json:"verdict"`
	DropReason   string    `json:"dropReason,omitempty"`
	Direction    string    `json:"direction"`
	SrcNamespace string    `json:"srcNamespace"`
	SrcPod       string    `json:"srcPod"`
	DstNamespace string    `json:"dstNamespace"`
	DstPod       string    `json:"dstPod"`
	Protocol     string    `json:"protocol"`
	DstPort      uint32    `json:"dstPort,omitempty"`
}

// HubbleStatus reports the availability and capacity of the Hubble Relay.
type HubbleStatus struct {
	Connected        bool    `json:"connected"`
	NumFlows         uint64  `json:"numFlows"`
	MaxFlows         uint64  `json:"maxFlows"`
	FlowsRate        float64 `json:"flowsRate"`
	ConnectedNodes   uint32  `json:"connectedNodes"`
	UnavailableNodes uint32  `json:"unavailableNodes"`
	Version          string  `json:"version"`
}

// HubbleClient wraps a gRPC connection to Hubble Relay.
type HubbleClient struct {
	conn   *grpc.ClientConn
	client observerpb.ObserverClient
}

// NewHubbleClient connects to Hubble Relay at the given address (e.g., "hubble-relay:80").
// Uses insecure credentials for in-cluster communication.
func NewHubbleClient(relayAddr string) (*HubbleClient, error) {
	conn, err := grpc.NewClient(relayAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to hubble relay at %s: %w", relayAddr, err)
	}
	return &HubbleClient{
		conn:   conn,
		client: observerpb.NewObserverClient(conn),
	}, nil
}

// Status checks Hubble Relay availability and returns capacity info.
func (c *HubbleClient) Status(ctx context.Context) (*HubbleStatus, error) {
	resp, err := c.client.ServerStatus(ctx, &observerpb.ServerStatusRequest{})
	if err != nil {
		return &HubbleStatus{Connected: false}, err
	}

	status := &HubbleStatus{
		Connected: true,
		NumFlows:  resp.GetNumFlows(),
		MaxFlows:  resp.GetMaxFlows(),
		FlowsRate: resp.GetFlowsRate(),
		Version:   resp.GetVersion(),
	}
	if n := resp.GetNumConnectedNodes(); n != nil {
		status.ConnectedNodes = n.GetValue()
	}
	if n := resp.GetNumUnavailableNodes(); n != nil {
		status.UnavailableNodes = n.GetValue()
	}
	return status, nil
}

// GetFlows queries Hubble Relay for recent flows matching the given filters.
// namespace is required. verdict and limit are optional (empty/0 = no filter/default 100).
func (c *HubbleClient) GetFlows(ctx context.Context, namespace, verdict string, limit int) ([]FlowRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	// Build whitelist filters: capture traffic where src OR dst is in the namespace
	var whitelist []*flowpb.FlowFilter
	srcFilter := &flowpb.FlowFilter{
		SourcePod: []string{namespace + "/"},
	}
	dstFilter := &flowpb.FlowFilter{
		DestinationPod: []string{namespace + "/"},
	}

	if verdict != "" {
		v, ok := verdictFromString(verdict)
		if ok {
			srcFilter.Verdict = []flowpb.Verdict{v}
			dstFilter.Verdict = []flowpb.Verdict{v}
		}
	}

	whitelist = append(whitelist, srcFilter, dstFilter)

	req := &observerpb.GetFlowsRequest{
		Number:    uint64(limit),
		Whitelist: whitelist,
	}

	stream, err := c.client.GetFlows(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("getting flows: %w", err)
	}

	var flows []FlowRecord
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Partial results are fine — return what we have
			if len(flows) > 0 {
				break
			}
			return nil, fmt.Errorf("receiving flow: %w", err)
		}

		f := resp.GetFlow()
		if f == nil {
			continue // skip node_status and lost_events
		}

		flows = append(flows, convertFlow(f))
	}

	return flows, nil
}

// Close closes the gRPC connection.
func (c *HubbleClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

func convertFlow(f *flowpb.Flow) FlowRecord {
	rec := FlowRecord{
		Verdict:   f.GetVerdict().String(),
		Direction: f.GetTrafficDirection().String(),
	}

	if t := f.GetTime(); t != nil {
		rec.Time = t.AsTime()
	}

	if dr := f.GetDropReasonDesc(); dr != flowpb.DropReason_DROP_REASON_UNKNOWN {
		rec.DropReason = dr.String()
	}

	if src := f.GetSource(); src != nil {
		rec.SrcNamespace = src.GetNamespace()
		rec.SrcPod = src.GetPodName()
	}

	if dst := f.GetDestination(); dst != nil {
		rec.DstNamespace = dst.GetNamespace()
		rec.DstPod = dst.GetPodName()
	}

	if l4 := f.GetL4(); l4 != nil {
		switch p := l4.GetProtocol().(type) {
		case *flowpb.Layer4_TCP:
			rec.Protocol = "TCP"
			rec.DstPort = p.TCP.GetDestinationPort()
		case *flowpb.Layer4_UDP:
			rec.Protocol = "UDP"
			rec.DstPort = p.UDP.GetDestinationPort()
		case *flowpb.Layer4_ICMPv4:
			rec.Protocol = "ICMPv4"
		case *flowpb.Layer4_ICMPv6:
			rec.Protocol = "ICMPv6"
		case *flowpb.Layer4_SCTP:
			rec.Protocol = "SCTP"
			rec.DstPort = p.SCTP.GetDestinationPort()
		}
	}

	return rec
}

func verdictFromString(s string) (flowpb.Verdict, bool) {
	switch s {
	case "FORWARDED":
		return flowpb.Verdict_FORWARDED, true
	case "DROPPED":
		return flowpb.Verdict_DROPPED, true
	case "ERROR":
		return flowpb.Verdict_ERROR, true
	case "AUDIT":
		return flowpb.Verdict_AUDIT, true
	default:
		return flowpb.Verdict_VERDICT_UNKNOWN, false
	}
}
