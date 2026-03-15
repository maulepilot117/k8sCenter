/**
 * Shared types used across wizard components.
 * Centralizes common type definitions to prevent duplication and drift.
 */

/** A key-value pair used for Kubernetes labels and selectors. */
export interface LabelEntry {
  key: string;
  value: string;
}

/**
 * A key-value pair used for Kubernetes selectors.
 * Structurally identical to LabelEntry but semantically distinct.
 */
export type SelectorEntry = LabelEntry;

/** Health probe configuration for a container. */
export interface ProbeState {
  type: string;
  path: string;
  port: number;
  initialDelaySeconds: number;
  periodSeconds: number;
}
