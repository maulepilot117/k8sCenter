// Drawer navigation catalog. Mirrors a subset of
// `frontend/lib/constants.ts::DOMAIN_SECTIONS`. PR-1d shipped the 6 hot
// kinds; PR-1e extends with ReplicaSet, StatefulSet, DaemonSet, Ingress,
// PVC (new Storage section), Namespace.

import 'package:flutter/material.dart';

class DomainKind {
  const DomainKind({
    required this.kind,
    required this.label,
    required this.icon,
    this.namespaced = true,
    this.customListPath,
  });

  /// URL path segment that maps to the backend's `kind` route param.
  final String kind;
  final String label;
  final IconData icon;

  /// True when the resource is namespace-scoped. Used by `kindDetailPath`
  /// to choose between `/clusters/<id>/<section>/<kind>/<ns>/<name>` and
  /// the cluster-scoped `/<kind>/<name>` shape.
  final bool namespaced;

  /// Optional override for the drawer's list-route navigation. Use the
  /// `{clusterId}` placeholder where the active cluster id should be
  /// substituted at navigation time. Entries that aren't resource kinds
  /// (Log search, Observability dashboards) use this so the section
  /// loop in the drawer doesn't have to special-case them.
  ///
  /// Resource kinds leave this null and the drawer falls back to
  /// `/clusters/<id>/<sectionPath>/<kind>`.
  final String? customListPath;
}

class DomainSection {
  const DomainSection({
    required this.label,
    required this.pathSegment,
    required this.icon,
    required this.kinds,
  });

  final String label;

  /// URL path segment that matches the route registered in app_router.dart
  /// (e.g., 'workloads', 'config', 'networking', 'cluster').
  final String pathSegment;

  final IconData icon;
  final List<DomainKind> kinds;
}

/// Kinds without a specialized screen fall through to the generic detail
/// at runtime via [GenericDetailScreen].
const List<DomainSection> domainSections = [
  DomainSection(
    label: 'Workloads',
    pathSegment: 'workloads',
    icon: Icons.layers_outlined,
    kinds: [
      DomainKind(kind: 'pods', label: 'Pods', icon: Icons.bubble_chart_outlined),
      DomainKind(
        kind: 'deployments',
        label: 'Deployments',
        icon: Icons.dashboard_outlined,
      ),
      DomainKind(
        kind: 'replicasets',
        label: 'ReplicaSets',
        icon: Icons.layers_outlined,
      ),
      DomainKind(
        kind: 'statefulsets',
        label: 'StatefulSets',
        icon: Icons.storage_outlined,
      ),
      DomainKind(
        kind: 'daemonsets',
        label: 'DaemonSets',
        icon: Icons.workspaces_outline,
      ),
    ],
  ),
  DomainSection(
    label: 'Networking',
    pathSegment: 'networking',
    icon: Icons.hub_outlined,
    kinds: [
      DomainKind(kind: 'services', label: 'Services', icon: Icons.lan_outlined),
      DomainKind(
        kind: 'ingresses',
        label: 'Ingresses',
        icon: Icons.alt_route_outlined,
      ),
    ],
  ),
  DomainSection(
    label: 'Configuration',
    pathSegment: 'config',
    icon: Icons.settings_outlined,
    kinds: [
      DomainKind(
        kind: 'configmaps',
        label: 'ConfigMaps',
        icon: Icons.description_outlined,
      ),
      DomainKind(
        kind: 'secrets',
        label: 'Secrets',
        icon: Icons.key_outlined,
      ),
    ],
  ),
  DomainSection(
    label: 'Storage',
    pathSegment: 'storage',
    icon: Icons.sd_storage_outlined,
    kinds: [
      DomainKind(
        kind: 'pvcs',
        label: 'PVCs',
        icon: Icons.sd_storage_outlined,
      ),
    ],
  ),
  DomainSection(
    label: 'Cluster',
    pathSegment: 'cluster',
    icon: Icons.dns_outlined,
    kinds: [
      DomainKind(
        kind: 'nodes',
        label: 'Nodes',
        icon: Icons.dns_outlined,
        namespaced: false,
      ),
      DomainKind(
        kind: 'namespaces',
        label: 'Namespaces',
        icon: Icons.folder_outlined,
        namespaced: false,
      ),
    ],
  ),
  // Observability surfaces — log search (PR-4c) and (future) policy /
  // mesh / cert-manager dashboards. These aren't resource kinds (no
  // /v1/resources/ endpoint backs them) so they wire through the
  // customListPath escape hatch rather than the kind/namespace
  // detail-route construction in `kindDetailPath`.
  DomainSection(
    label: 'Observability',
    pathSegment: 'observability',
    icon: Icons.insights_outlined,
    kinds: [
      DomainKind(
        kind: 'logs',
        label: 'Log search',
        icon: Icons.text_snippet_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/logs',
      ),
    ],
  ),
  // GitOps surfaces — Argo CD + Flux Applications and Argo CD
  // ApplicationSets. Both entries are status-gated at screen mount via
  // `gitOpsStatusProvider` rather than at the drawer level — operators
  // on a cluster without GitOps see the entries but land on
  // `FeatureUnavailableState.gitops()` when they tap.
  DomainSection(
    label: 'GitOps',
    pathSegment: 'gitops',
    icon: Icons.account_tree_outlined,
    kinds: [
      DomainKind(
        kind: 'applications',
        label: 'Applications',
        icon: Icons.dashboard_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/gitops/applications',
      ),
      DomainKind(
        kind: 'applicationsets',
        label: 'ApplicationSets',
        icon: Icons.account_tree_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/gitops/applicationsets',
      ),
    ],
  ),
];

/// Substitutes `{clusterId}` (and any future placeholder) in a
/// [DomainKind.customListPath] template against the active cluster id.
/// Pulled out as a top-level so the drawer + any future deep-link
/// builder share one helper.
String resolveCustomListPath(String template, {required String clusterId}) {
  return template.replaceAll('{clusterId}', clusterId);
}

/// Lookup the section that owns a given kind. Used by `kindDetailPath`
/// so detail-route construction stays in one place rather than each
/// screen hardcoding its parent section.
DomainSection? findDomainSection(String kind) {
  for (final section in domainSections) {
    for (final k in section.kinds) {
      if (k.kind == kind) return section;
    }
  }
  return null;
}

/// Builds the detail-route URL for a given kind. Namespaced kinds get
/// `/clusters/<id>/<section>/<kind>/<namespace>/<name>`; cluster-scoped
/// kinds get `/clusters/<id>/<section>/<kind>/<name>`. Falls through to
/// the generic-detail catch-all when the kind is not in [domainSections].
///
/// Pulling all path construction here means cluster-id propagation, kind
/// → section mapping, and the namespaced-vs-cluster-scoped shape all
/// live in one source of truth — no per-screen hardcoded URLs.
String kindDetailPath({
  required String clusterId,
  required String kind,
  required String namespace,
  required String name,
}) {
  final section = findDomainSection(kind);
  if (section == null) {
    // Generic catch-all uses '_' as the cluster-scoped namespace
    // sentinel — picked because '_' is not a legal Kubernetes namespace
    // (DNS-1123 label, can't start with underscore) so it can't collide.
    final ns = namespace.isEmpty ? '_' : namespace;
    return '/clusters/$clusterId/generic/$kind/$ns/$name';
  }
  final k = section.kinds.firstWhere((k) => k.kind == kind);
  if (k.namespaced) {
    return '/clusters/$clusterId/${section.pathSegment}/$kind/$namespace/$name';
  }
  return '/clusters/$clusterId/${section.pathSegment}/$kind/$name';
}

/// Sentinel used in the generic-detail route's `:namespace` slot to
/// signal a cluster-scoped resource. `_` is illegal in DNS-1123 labels
/// so it cannot collide with a real namespace name (unlike `cluster`,
/// which is a perfectly valid namespace identifier).
const String clusterScopedNamespaceSentinel = '_';
