// Wizard registry — catalogue of mobile-supported wizard types.
//
// Mirrors the implicit catalogue of `frontend/islands/*Wizard.tsx`. Each
// entry carries the metadata the drawer needs to render and RBAC-gate a
// "Create" entry: backend wizard type, produced kind, scope, label,
// route hint, and the verb that gates visibility.
//
// Stability rule: drift between this registry and the web wizard
// catalogue is exactly the bug class M2's web/Dart isomorphism
// discipline exists to prevent. When either codebase adds a wizard,
// update both in the same PR.
//
// Build progress: PR-3a registers the three wizards that ship in this
// PR (configmap, secret, service). PRs 3b–3e fill in the rest. Adding
// an entry here without a matching `WizardEntry.routeBuilder` will
// throw at navigation time — better to fail loud than to render a
// drawer entry that 404s.

import 'package:flutter/material.dart';

import '../auth/permissions.dart';
import '../auth/user.dart';

/// Cluster vs namespaced wizards. Cluster-scoped wizards (ClusterIssuer,
/// ClusterSecretStore, NetworkPolicy when treated cluster-wide, etc.)
/// don't render a namespace input on the Identity step and check RBAC
/// against the cluster-scoped permissions slot.
enum WizardScope { namespaced, cluster }

class WizardEntry {
  const WizardEntry({
    required this.type,
    required this.scope,
    required this.label,
    required this.kind,
    required this.icon,
    required this.group,
    this.createVerb = 'create',
    this.requiresNamespace = true,
  });

  /// Backend wizard type — value used in `/v1/wizards/:type/preview`.
  final String type;

  /// Display label rendered in the drawer's Create submenu.
  final String label;

  /// Kubernetes kind this wizard produces (lowercase plural to match the
  /// `kind` URL param used everywhere else in the app, e.g. `configmaps`,
  /// `secrets`, `services`). RBAC checks against this kind.
  final String kind;

  /// Section the entry belongs to in the drawer (e.g., `Configuration`,
  /// `Workloads`, `Networking`). Mirrors `domain_sections.dart`.
  final String group;

  /// Drawer icon.
  final IconData icon;

  final WizardScope scope;

  /// k8s verb required to see this entry. Defaults to `create`; overrides
  /// exist for wizards that produce multiple resources or rely on
  /// non-create permissions (none in PR-3a).
  final String createVerb;

  /// Whether the wizard requires the operator to be in a namespace
  /// context. When false (cluster-scoped wizards) the namespace input is
  /// hidden and RBAC checks against `clusterScoped` only.
  final bool requiresNamespace;
}

/// All 28 routable wizard types from the web frontend. PR-3a ships
/// concrete screens for `configmap`, `secret`, and `service`; PR-3b–3e
/// fill in the rest. Operators tapping an un-built entry land on the
/// `_ComingSoonScreen` placeholder that wizard_routes.dart serves for
/// any registered type without a concrete screen. Pre-registering all
/// 28 entries here satisfies R10 (web/Dart isomorphism) — drift is now
/// caught by reviewers reading both files in the same PR rather than
/// silently lagging.
///
/// Order: Configuration / Networking groups first (PR-3a's live
/// entries), then Workloads (PR-3b), then Storage / Backup (PR-3d),
/// then Networking continuations (PR-3c), then CRD wizards (PR-3e).
/// Within each group, the natural creation order mirrors the web
/// frontend's drawer.
const List<WizardEntry> wizardRegistry = [
  // --- Configuration ---
  WizardEntry(
    type: 'configmap',
    scope: WizardScope.namespaced,
    label: 'ConfigMap',
    kind: 'configmaps',
    icon: Icons.description_outlined,
    group: 'Configuration',
  ),
  WizardEntry(
    type: 'secret',
    scope: WizardScope.namespaced,
    label: 'Secret',
    kind: 'secrets',
    icon: Icons.key_outlined,
    group: 'Configuration',
  ),

  // --- Networking ---
  WizardEntry(
    type: 'service',
    scope: WizardScope.namespaced,
    label: 'Service',
    kind: 'services',
    icon: Icons.lan_outlined,
    group: 'Networking',
  ),
  WizardEntry(
    type: 'ingress',
    scope: WizardScope.namespaced,
    label: 'Ingress',
    kind: 'ingresses',
    icon: Icons.alt_route_outlined,
    group: 'Networking',
  ),
  WizardEntry(
    type: 'networkpolicy',
    scope: WizardScope.namespaced,
    label: 'NetworkPolicy',
    kind: 'networkpolicies',
    icon: Icons.policy_outlined,
    group: 'Networking',
  ),

  // --- Workloads ---
  WizardEntry(
    type: 'deployment',
    scope: WizardScope.namespaced,
    label: 'Deployment',
    kind: 'deployments',
    icon: Icons.dashboard_outlined,
    group: 'Workloads',
  ),
  WizardEntry(
    type: 'job',
    scope: WizardScope.namespaced,
    label: 'Job',
    kind: 'jobs',
    icon: Icons.work_outline,
    group: 'Workloads',
  ),
  WizardEntry(
    type: 'cronjob',
    scope: WizardScope.namespaced,
    label: 'CronJob',
    kind: 'cronjobs',
    icon: Icons.schedule_outlined,
    group: 'Workloads',
  ),
  WizardEntry(
    type: 'daemonset',
    scope: WizardScope.namespaced,
    label: 'DaemonSet',
    kind: 'daemonsets',
    icon: Icons.workspaces_outline,
    group: 'Workloads',
  ),
  WizardEntry(
    type: 'statefulset',
    scope: WizardScope.namespaced,
    label: 'StatefulSet',
    kind: 'statefulsets',
    icon: Icons.storage_outlined,
    group: 'Workloads',
  ),

  // --- Scaling & Reliability ---
  WizardEntry(
    type: 'hpa',
    scope: WizardScope.namespaced,
    label: 'HorizontalPodAutoscaler',
    kind: 'horizontalpodautoscalers',
    icon: Icons.trending_up_outlined,
    group: 'Scaling',
  ),
  WizardEntry(
    type: 'pdb',
    scope: WizardScope.namespaced,
    label: 'PodDisruptionBudget',
    kind: 'poddisruptionbudgets',
    icon: Icons.shield_outlined,
    group: 'Scaling',
  ),

  // --- RBAC ---
  WizardEntry(
    type: 'rolebinding',
    scope: WizardScope.namespaced,
    label: 'RoleBinding',
    kind: 'rolebindings',
    icon: Icons.lock_person_outlined,
    group: 'RBAC',
  ),

  // --- Storage ---
  WizardEntry(
    type: 'pvc',
    scope: WizardScope.namespaced,
    label: 'PersistentVolumeClaim',
    kind: 'persistentvolumeclaims',
    icon: Icons.sd_storage_outlined,
    group: 'Storage',
  ),
  WizardEntry(
    type: 'storageclass',
    scope: WizardScope.cluster,
    label: 'StorageClass',
    kind: 'storageclasses',
    icon: Icons.dns_outlined,
    group: 'Storage',
    requiresNamespace: false,
  ),
  WizardEntry(
    type: 'snapshot',
    scope: WizardScope.namespaced,
    label: 'VolumeSnapshot',
    kind: 'volumesnapshots',
    icon: Icons.photo_camera_outlined,
    group: 'Storage',
  ),
  WizardEntry(
    type: 'scheduled-snapshot',
    scope: WizardScope.namespaced,
    label: 'Scheduled snapshot',
    kind: 'schedules',
    icon: Icons.update_outlined,
    group: 'Storage',
  ),

  // --- Quotas ---
  WizardEntry(
    type: 'namespace-limits',
    scope: WizardScope.namespaced,
    label: 'Namespace limits (Quota + LimitRange)',
    kind: 'resourcequotas',
    icon: Icons.tune_outlined,
    group: 'Quotas',
  ),

  // --- Backup (Velero) ---
  WizardEntry(
    type: 'velero-backup',
    scope: WizardScope.namespaced,
    label: 'Velero Backup',
    kind: 'backups',
    icon: Icons.backup_outlined,
    group: 'Backup',
  ),
  WizardEntry(
    type: 'velero-restore',
    scope: WizardScope.namespaced,
    label: 'Velero Restore',
    kind: 'restores',
    icon: Icons.restore_outlined,
    group: 'Backup',
  ),
  WizardEntry(
    type: 'velero-schedule',
    scope: WizardScope.namespaced,
    label: 'Velero Schedule',
    kind: 'schedules',
    icon: Icons.event_repeat_outlined,
    group: 'Backup',
  ),

  // --- Cert-Manager ---
  WizardEntry(
    type: 'certificate',
    scope: WizardScope.namespaced,
    label: 'Certificate',
    kind: 'certificates',
    icon: Icons.verified_user_outlined,
    group: 'Certificates',
  ),
  WizardEntry(
    type: 'issuer',
    scope: WizardScope.namespaced,
    label: 'Issuer',
    kind: 'issuers',
    icon: Icons.workspace_premium_outlined,
    group: 'Certificates',
  ),
  WizardEntry(
    type: 'cluster-issuer',
    scope: WizardScope.cluster,
    label: 'ClusterIssuer',
    kind: 'clusterissuers',
    icon: Icons.workspace_premium_outlined,
    group: 'Certificates',
    requiresNamespace: false,
  ),

  // --- External Secrets Operator ---
  WizardEntry(
    type: 'external-secret',
    scope: WizardScope.namespaced,
    label: 'ExternalSecret',
    kind: 'externalsecrets',
    icon: Icons.lock_outlined,
    group: 'External Secrets',
  ),
  WizardEntry(
    type: 'secret-store',
    scope: WizardScope.namespaced,
    label: 'SecretStore',
    kind: 'secretstores',
    icon: Icons.vpn_key_outlined,
    group: 'External Secrets',
  ),
  WizardEntry(
    type: 'cluster-secret-store',
    scope: WizardScope.cluster,
    label: 'ClusterSecretStore',
    kind: 'clustersecretstores',
    icon: Icons.vpn_key_outlined,
    group: 'External Secrets',
    requiresNamespace: false,
  ),

  // --- Policy ---
  WizardEntry(
    type: 'policy',
    scope: WizardScope.cluster,
    label: 'Policy',
    kind: 'clusterpolicies',
    icon: Icons.gavel_outlined,
    group: 'Policy',
    requiresNamespace: false,
  ),
];

/// Filter the registry to entries the operator's RBAC permits. Mirrors
/// the drawer's read-side gating in `domain_navigation_drawer.dart`.
///
/// [namespace] is the operator's currently-active namespace. For
/// cluster-scoped wizards, namespace is ignored. For namespaced
/// wizards, an empty namespace falls through to "any namespace where I
/// have create" via [canPerform]'s `allowAnyNamespaceFallback` so the
/// drawer doesn't lock out an operator who hasn't picked a namespace
/// yet.
List<WizardEntry> visibleWizards({
  required RBACSummary? rbac,
  required String namespace,
}) {
  final out = <WizardEntry>[];
  for (final entry in wizardRegistry) {
    final ok = canPerform(
      rbac,
      entry.kind,
      entry.createVerb,
      entry.requiresNamespace ? namespace : '',
      allowAnyNamespaceFallback: entry.requiresNamespace,
    );
    if (ok) out.add(entry);
  }
  return out;
}

/// Convenience lookup. Returns null if unregistered. Used by
/// `wizard_routes.dart` to derive the wizard's metadata from `:type` so
/// the router doesn't duplicate the registry's mapping.
WizardEntry? findWizardEntry(String type) {
  for (final e in wizardRegistry) {
    if (e.type == type) return e;
  }
  return null;
}
