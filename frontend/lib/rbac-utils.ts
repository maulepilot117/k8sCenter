import {
  CLUSTER_SCOPED_KINDS,
  RESOURCE_DETAIL_PATHS,
} from "@/lib/constants.ts";

/** Build the detail page URL for a roleRef (Role or ClusterRole). */
export function resolveRoleHref(
  roleRefKind: string,
  roleRefName: string,
  namespace?: string,
): string {
  const kindKey = roleRefKind.toLowerCase() + "s"; // ClusterRole → clusterroles
  const path = RESOURCE_DETAIL_PATHS[kindKey];
  if (!path) return "#";
  return CLUSTER_SCOPED_KINDS.has(kindKey)
    ? `${path}/${roleRefName}`
    : `${path}/${namespace}/${roleRefName}`;
}

/** A single effective permission rule with its source attribution. */
export interface EffectiveRule {
  apiGroups: string[];
  resources: string[];
  verbs: string[];
  source: string; // e.g. "Role: developer (via dev-binding in default)"
}

/** A binding with its resolved role rules (for the overview table). */
export interface ResolvedBinding {
  subjectKind: string;
  subjectName: string;
  subjectNamespace?: string;
  bindingKind: string;
  bindingName: string;
  bindingNamespace?: string;
  roleKind: string;
  roleName: string;
  rulesCount: number;
}

interface RoleRef {
  kind: string;
  name: string;
  apiGroup: string;
}

interface Subject {
  kind: string;
  name: string;
  namespace?: string;
}

interface PolicyRule {
  apiGroups?: string[];
  resources?: string[];
  verbs?: string[];
}

interface BindingLike {
  metadata: { name: string; namespace?: string };
  roleRef: RoleRef;
  subjects?: Subject[];
}

interface RoleLike {
  metadata: { name: string; namespace?: string };
  rules?: PolicyRule[];
}

/**
 * Build a flat array of resolved bindings from raw RBAC resources.
 * Each entry links a subject to a binding to a role with a rules count.
 */
export function resolveBindings(
  bindings: BindingLike[],
  roles: Map<string, RoleLike>,
): ResolvedBinding[] {
  const result: ResolvedBinding[] = [];

  for (const b of bindings) {
    if (!b.subjects) continue;

    const roleKey = b.roleRef.kind === "ClusterRole"
      ? `cluster:${b.roleRef.name}`
      : `${b.metadata.namespace}:${b.roleRef.name}`;
    const role = roles.get(roleKey);

    for (const s of b.subjects) {
      result.push({
        subjectKind: s.kind,
        subjectName: s.name,
        subjectNamespace: s.namespace,
        bindingKind: b.metadata.namespace
          ? "RoleBinding"
          : "ClusterRoleBinding",
        bindingName: b.metadata.name,
        bindingNamespace: b.metadata.namespace,
        roleKind: b.roleRef.kind,
        roleName: b.roleRef.name,
        rulesCount: role?.rules?.length ?? 0,
      });
    }
  }

  return result;
}

/**
 * Compute effective permissions for a specific subject across all bindings.
 * Returns raw rule union with source attribution — no semantic merging.
 */
export function computeEffectivePermissions(
  subjectName: string,
  bindings: BindingLike[],
  roles: Map<string, RoleLike>,
): EffectiveRule[] {
  const result: EffectiveRule[] = [];

  for (const b of bindings) {
    if (!b.subjects?.some((s) => s.name === subjectName)) continue;

    const roleKey = b.roleRef.kind === "ClusterRole"
      ? `cluster:${b.roleRef.name}`
      : `${b.metadata.namespace}:${b.roleRef.name}`;
    const role = roles.get(roleKey);

    if (!role?.rules) {
      // aggregationRule ClusterRoles have no inline rules
      result.push({
        apiGroups: ["*"],
        resources: ["(aggregated)"],
        verbs: ["(see child roles)"],
        source: `${b.roleRef.kind}: ${b.roleRef.name} (via ${b.metadata.name}${
          b.metadata.namespace ? ` in ${b.metadata.namespace}` : ""
        })`,
      });
      continue;
    }

    for (const rule of role.rules) {
      result.push({
        apiGroups: rule.apiGroups ?? [""],
        resources: rule.resources ?? [],
        verbs: rule.verbs ?? [],
        source: `${b.roleRef.kind}: ${b.roleRef.name} (via ${b.metadata.name}${
          b.metadata.namespace ? ` in ${b.metadata.namespace}` : ""
        })`,
      });
    }
  }

  return result;
}
