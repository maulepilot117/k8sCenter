// Response types for Cilium networking overview endpoints.
// Uses discriminated unions for proper TypeScript narrowing on `configured`.

import type { SourceCoverage } from "@/lib/k8s-types.ts";

export type CiliumBGPResponse =
  | { configured: false }
  | {
      configured: true;
      peers: BGPPeerStatus[];
    };

export type CiliumIPAMResponse =
  | { configured: false }
  | {
      configured: true;
      mode: string;
      podCIDRs: string[];
      allocated: number;
      available: number;
      total: number;
      exhaustionRisk: string;
      perNode: NodeIPAM[];
    };

export type CiliumSubsystemsResponse =
  | { configured: false }
  | {
      configured: true;
      encryption: EncryptionInfo;
      mesh: MeshInfo;
      clusterMesh: ClusterMeshInfo;
      /** Absent when a remote cluster could not provide the endpoint list. */
      endpoints?: EndpointCounts;
      /**
       * Each source a remote cluster could not provide; the sections built
       * from it are omitted or partial. Never set on the local cluster.
       */
      coverage?: SourceCoverage[];
    };

export interface BGPPeerStatus {
  node: string;
  peerAddress: string;
  peerASN: number;
  localASN: number;
  sessionState: string;
  routesReceived: number;
  routesAdvertised: number;
}

export interface NodeIPAM {
  node: string;
  allocated: number;
  available: number;
  podCIDR: string;
}

export interface EncryptionInfo {
  enabled: boolean;
  mode: string;
  nodesEncrypted: number;
  nodesTotal: number;
  wireGuardNodes?: WireGuardNode[];
}

export interface WireGuardNode {
  nodeName: string;
  publicKey: string;
  listenPort: number;
  peerCount: number;
  peers: WireGuardPeer[];
}

export interface WireGuardPeer {
  publicKey: string;
  endpoint: string;
  lastHandshake: string;
  transferRx: number;
  transferTx: number;
}

export interface MeshInfo {
  enabled: boolean;
  engine: string;
  deploymentMode?: string;
  totalRedirects?: number;
  totalPorts?: number;
}

export interface ClusterMeshInfo {
  enabled: boolean;
  remoteClusters?: RemoteCluster[];
}

export interface RemoteCluster {
  name: string;
  connected: boolean;
  ready: boolean;
  status: string;
  numNodes: number;
  numEndpoints: number;
  numSharedServices: number;
  numFailures: number;
  lastFailure?: string;
}

export interface EndpointCounts {
  total: number;
  ready: number;
  notReady: number;
  disconnecting: number;
  waiting: number;
}

// Connectivity response — discriminated union matching BGP/IPAM/Subsystems pattern.
export type CiliumConnectivityResponse =
  | { configured: false }
  | {
      configured: true;
      execEnabled: boolean;
      nodes: NodeConnectivity[];
      collectedAt?: string;
      partial?: boolean;
    };

export interface NodeConnectivity {
  nodeName: string;
  healthState: string;
  message?: string;
  /**
   * Set when a remote cluster's agent on this node could not be reached by
   * exec (forbidden, timeout, pod_gone, failed). The node's health is then
   * unknown: the exec failed, not the node.
   */
  execError?: string;
}
