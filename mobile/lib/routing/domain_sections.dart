// Drawer navigation catalog. Mirrors a subset of
// `frontend/lib/constants.ts::DOMAIN_SECTIONS` — only the kinds with
// specialized list/detail screens in PR-1d are wired here. Subsequent PRs
// (PR-1e adds RS/STS/DS/Ingress/PVC/Namespace) extend the list.

import 'package:flutter/material.dart';

class DomainKind {
  const DomainKind({
    required this.kind,
    required this.label,
    required this.icon,
    this.namespaced = true,
  });

  /// URL path segment that maps to the backend's `kind` route param.
  final String kind;
  final String label;
  final IconData icon;

  /// True when the resource is namespace-scoped. Used by `kindDetailPath`
  /// to choose between `/clusters/<id>/<section>/<kind>/<ns>/<name>` and
  /// the cluster-scoped `/<kind>/<name>` shape.
  final bool namespaced;
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

/// PR-1d slice. Kinds without a specialized screen fall through to the
/// generic detail at runtime.
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
    ],
  ),
  DomainSection(
    label: 'Networking',
    pathSegment: 'networking',
    icon: Icons.hub_outlined,
    kinds: [
      DomainKind(kind: 'services', label: 'Services', icon: Icons.lan_outlined),
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
    ],
  ),
];

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
