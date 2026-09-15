/**
 * Namespace quota presets, shared by both frontends.
 *
 * These lived inside `islands/NamespaceLimitsWizard.tsx`, and
 * `components/wizard/NamespacePresetStep.tsx` imported the value back out of
 * that island. That back-edge made the island a module the step depends on,
 * so the ported `src/islands/NamespaceLimitsWizard.tsx` -- which imports the
 * step from the shared components tree -- dragged the pre-migration island
 * into the same bundle. Two copies of a wizard island, only one of which the
 * router ever mounts.
 *
 * Frozen configuration data has no business living inside a component for
 * exactly this reason. It is a leaf module now: both islands and the step
 * import it, nothing imports them back, and the cycle is gone.
 */

export const PRESETS = {
  small: {
    label: "Small",
    description: "For development or small workloads",
    quota: { cpuHard: "2", memoryHard: "4Gi", podsHard: 10 },
    limits: {
      containerDefault: { cpu: "100m", memory: "128Mi" },
      containerDefaultRequest: { cpu: "50m", memory: "64Mi" },
      containerMax: { cpu: "1", memory: "2Gi" },
      containerMin: { cpu: "10m", memory: "8Mi" },
    },
  },
  standard: {
    label: "Standard",
    description: "For typical production workloads",
    quota: { cpuHard: "8", memoryHard: "16Gi", podsHard: 20 },
    limits: {
      containerDefault: { cpu: "250m", memory: "256Mi" },
      containerDefaultRequest: { cpu: "100m", memory: "128Mi" },
      containerMax: { cpu: "2", memory: "4Gi" },
      containerMin: { cpu: "10m", memory: "8Mi" },
    },
  },
  large: {
    label: "Large",
    description: "For resource-intensive workloads",
    quota: { cpuHard: "32", memoryHard: "64Gi", podsHard: 100 },
    limits: {
      containerDefault: { cpu: "500m", memory: "512Mi" },
      containerDefaultRequest: { cpu: "250m", memory: "256Mi" },
      containerMax: { cpu: "4", memory: "8Gi" },
      containerMin: { cpu: "10m", memory: "8Mi" },
    },
  },
  custom: {
    label: "Custom",
    description: "Configure all values manually",
    quota: { cpuHard: "4", memoryHard: "8Gi", podsHard: 20 },
    limits: {
      containerDefault: { cpu: "200m", memory: "256Mi" },
      containerDefaultRequest: { cpu: "100m", memory: "128Mi" },
      containerMax: { cpu: "2", memory: "4Gi" },
      containerMin: { cpu: "10m", memory: "8Mi" },
    },
  },
} as const;

export type PresetKey = keyof typeof PRESETS;
