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

/// All wizard entries the drawer surfaces. PR-3a opens with three; later
/// PRs extend this list. Order within a group matches the order entries
/// will eventually appear after all milestones land — additions don't
/// reshuffle existing positions.
const List<WizardEntry> wizardRegistry = [
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
  WizardEntry(
    type: 'service',
    scope: WizardScope.namespaced,
    label: 'Service',
    kind: 'services',
    icon: Icons.lan_outlined,
    group: 'Networking',
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

/// Group entries by their `group` field, preserving registry order.
/// Drawer renders groups as section headings.
Map<String, List<WizardEntry>> groupByCategory(List<WizardEntry> entries) {
  final out = <String, List<WizardEntry>>{};
  for (final e in entries) {
    out.putIfAbsent(e.group, () => <WizardEntry>[]).add(e);
  }
  return out;
}

/// Convenience lookup. Returns null if unregistered.
WizardEntry? findWizardEntry(String type) {
  for (final e in wizardRegistry) {
    if (e.type == type) return e;
  }
  return null;
}
