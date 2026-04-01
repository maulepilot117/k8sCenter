/**
 * Shared types used across wizard components.
 * Centralizes common type definitions to prevent duplication and drift.
 */

/** A key-value pair used for Kubernetes labels and selectors. */
export interface LabelEntry {
  key: string;
  value: string;
}

/** A container port entry used in workload wizards. */
export interface PortEntry {
  containerPort: number;
  protocol: string;
}

/** An environment variable entry used in workload wizards. */
export interface EnvVarEntry {
  name: string;
  value: string;
  configMapRef: string;
  secretRef: string;
  key: string;
}

/** StorageClass item shape returned by the API. */
export interface StorageClassItem {
  metadata: { name: string };
  provisioner?: string;
}

/** Health probe configuration for a container. */
export interface ProbeState {
  type: string;
  path: string;
  port: number;
  initialDelaySeconds: number;
  periodSeconds: number;
}
