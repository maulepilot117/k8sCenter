// Response types for Cilium networking overview endpoints.
// Uses discriminated unions for proper TypeScript narrowing on `configured`.

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
    endpoints: EndpointCounts;
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
}

export interface MeshInfo {
  enabled: boolean;
  engine: string;
}

export interface ClusterMeshInfo {
  enabled: boolean;
}

export interface EndpointCounts {
  total: number;
  ready: number;
  notReady: number;
  disconnecting: number;
  waiting: number;
}
