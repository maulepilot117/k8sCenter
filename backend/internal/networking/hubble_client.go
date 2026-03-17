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

// HubbleClient wraps a gRPC connection to Hubble Relay.
type HubbleClient struct {
	conn   *grpc.ClientConn
	client observerpb.ObserverClient
}

// grpcDialTimeout is the maximum time to wait for the initial gRPC connection.
const grpcDialTimeout = 10 * time.Second

// grpcStreamTimeout is the maximum time to wait for a complete flow query.
const grpcStreamTimeout = 30 * time.Second

// NewHubbleClient connects to Hubble Relay at the given address (e.g., "hubble-relay:80").
// Uses insecure credentials for in-cluster communication.
func NewHubbleClient(relayAddr string) (*HubbleClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), grpcDialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, relayAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to hubble relay at %s: %w", relayAddr, err)
	}
	return &HubbleClient{
		conn:   conn,
		client: observerpb.NewObserverClient(conn),
	}, nil
}

// GetFlows queries Hubble Relay for recent flows matching the given filters.
// namespace is required. verdict and limit are optional (empty/0 = no filter/default 100).
func (c *HubbleClient) GetFlows(ctx context.Context, namespace, verdict string, limit int) ([]FlowRecord, error) {
	// Enforce stream timeout shorter than HTTP request timeout
	ctx, cancel := context.WithTimeout(ctx, grpcStreamTimeout)
	defer cancel()

	// Build whitelist filters: capture traffic where src OR dst is in the namespace
	srcFilter := &flowpb.FlowFilter{
		SourcePod: []string{namespace + "/"},
	}
	dstFilter := &flowpb.FlowFilter{
		DestinationPod: []string{namespace + "/"},
	}

	if verdict != "" {
		v, _ := verdictFromString(verdict)
		srcFilter.Verdict = []flowpb.Verdict{v}
		dstFilter.Verdict = []flowpb.Verdict{v}
	}

	req := &observerpb.GetFlowsRequest{
		Number:    uint64(limit),
		Whitelist: []*flowpb.FlowFilter{srcFilter, dstFilter},
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
			if len(flows) > 0 {
				break // partial results are fine
			}
			return nil, fmt.Errorf("receiving flow: %w", err)
		}

		f := resp.GetFlow()
		if f == nil {
			continue
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

// ValidVerdict returns true if the verdict string is a recognized Hubble verdict.
func ValidVerdict(s string) bool {
	_, ok := verdictFromString(s)
	return ok
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
