/** Metadata for a policy template displayed in the wizard. */
export interface PolicyTemplateInfo {
  id: string;
  name: string;
  category: string;
  description: string;
  severity: "high" | "medium" | "low";
  targetKinds: string[];
  engines: ("kyverno" | "gatekeeper")[];
  /** Field definitions for template-specific parameters. */
  paramFields: ParamField[];
  /** Default Kyverno action based on severity. */
  defaultKyvernoAction: "Enforce" | "Audit";
  /** Default Gatekeeper action based on severity. */
  defaultGatekeeperAction: "deny" | "dryrun" | "warn";
}

export interface ParamField {
  key: string;
  label: string;
  type: "boolean" | "stringList" | "string";
  description: string;
  defaultValue?: unknown;
  required?: boolean;
}

/** All policy categories in display order. */
export const POLICY_CATEGORIES = [
  "Pod Security",
  "Image Policies",
  "Resource Management",
  "Labeling",
] as const;

/** All 8 policy templates available in the wizard. */
export const POLICY_TEMPLATES: PolicyTemplateInfo[] = [
  // ── Pod Security ──
  {
    id: "disallow-privileged",
    name: "Disallow Privileged Containers",
    category: "Pod Security",
    description:
      "Privileged mode disables most security mechanisms and must not be allowed.",
    severity: "high",
    targetKinds: ["Pod"],
    engines: ["kyverno", "gatekeeper"],
    paramFields: [],
    defaultKyvernoAction: "Enforce",
    defaultGatekeeperAction: "deny",
  },
  {
    id: "disallow-root",
    name: "Disallow Root User",
    category: "Pod Security",
    description: "Containers must run as a non-root user.",
    severity: "high",
    targetKinds: ["Pod"],
    engines: ["kyverno", "gatekeeper"],
    paramFields: [],
    defaultKyvernoAction: "Enforce",
    defaultGatekeeperAction: "deny",
  },
  {
    id: "disallow-privilege-escalation",
    name: "Disallow Privilege Escalation",
    category: "Pod Security",
    description:
      "Containers must not allow privilege escalation via setuid or setgid.",
    severity: "high",
    targetKinds: ["Pod"],
    engines: ["kyverno", "gatekeeper"],
    paramFields: [],
    defaultKyvernoAction: "Enforce",
    defaultGatekeeperAction: "deny",
  },
  {
    id: "restrict-capabilities",
    name: "Restrict Capabilities",
    category: "Pod Security",
    description: "Drop all capabilities and allow only specific additions.",
    severity: "medium",
    targetKinds: ["Pod"],
    engines: ["kyverno", "gatekeeper"],
    paramFields: [
      {
        key: "dropAll",
        label: "Drop ALL capabilities",
        type: "boolean",
        description: "Require all containers to drop ALL capabilities.",
        defaultValue: true,
      },
      {
        key: "allowedAdd",
        label: "Allowed capabilities",
        type: "stringList",
        description: "Capabilities that may be added (e.g. NET_BIND_SERVICE).",
        defaultValue: [],
      },
    ],
    defaultKyvernoAction: "Audit",
    defaultGatekeeperAction: "dryrun",
  },
  // ── Image Policies ──
  {
    id: "allowed-registries",
    name: "Restrict Image Registries",
    category: "Image Policies",
    description: "Images must come from approved container registries.",
    severity: "high",
    targetKinds: ["Pod"],
    engines: ["kyverno", "gatekeeper"],
    paramFields: [
      {
        key: "registries",
        label: "Allowed registries",
        type: "stringList",
        description:
          'Registry prefixes that are allowed (e.g. "ghcr.io/", "registry.k8s.io/").',
        defaultValue: [],
        required: true,
      },
    ],
    defaultKyvernoAction: "Enforce",
    defaultGatekeeperAction: "deny",
  },
  {
    id: "disallow-latest-tag",
    name: "Disallow Latest Tag",
    category: "Image Policies",
    description: "Images must have an explicit tag; ':latest' is not allowed.",
    severity: "medium",
    targetKinds: ["Pod"],
    engines: ["kyverno", "gatekeeper"],
    paramFields: [],
    defaultKyvernoAction: "Audit",
    defaultGatekeeperAction: "dryrun",
  },
  // ── Resource Management ──
  {
    id: "require-resource-limits",
    name: "Require Resource Limits",
    category: "Resource Management",
    description: "All containers must define CPU and memory resource limits.",
    severity: "medium",
    targetKinds: ["Pod"],
    engines: ["kyverno", "gatekeeper"],
    paramFields: [
      {
        key: "requireCpu",
        label: "Require CPU limits",
        type: "boolean",
        description: "Require CPU limits on all containers.",
        defaultValue: true,
      },
      {
        key: "requireMemory",
        label: "Require memory limits",
        type: "boolean",
        description: "Require memory limits on all containers.",
        defaultValue: true,
      },
    ],
    defaultKyvernoAction: "Audit",
    defaultGatekeeperAction: "dryrun",
  },
  // ── Labeling ──
  {
    id: "require-labels",
    name: "Require Labels",
    category: "Labeling",
    description:
      "Resources must have specified labels for ownership and lifecycle tracking.",
    severity: "medium",
    targetKinds: ["Pod", "Deployment", "StatefulSet", "DaemonSet"],
    engines: ["kyverno", "gatekeeper"],
    paramFields: [
      {
        key: "labels",
        label: "Required labels",
        type: "stringList",
        description:
          'Label keys that must be present (e.g. "app.kubernetes.io/name").',
        defaultValue: [],
        required: true,
      },
    ],
    defaultKyvernoAction: "Audit",
    defaultGatekeeperAction: "dryrun",
  },
];

/** Get templates grouped by category. */
export function getTemplatesByCategory(): Map<string, PolicyTemplateInfo[]> {
  const grouped = new Map<string, PolicyTemplateInfo[]>();
  for (const cat of POLICY_CATEGORIES) {
    grouped.set(cat, POLICY_TEMPLATES.filter((t) => t.category === cat));
  }
  return grouped;
}

/** Find a template by ID. */
export function getTemplate(
  id: string,
): PolicyTemplateInfo | undefined {
  return POLICY_TEMPLATES.find((t) => t.id === id);
}
