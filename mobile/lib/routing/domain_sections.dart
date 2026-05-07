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

/// Lookup a kind by its URL path segment. Returns null when the kind is
/// not in the catalog (caller falls through to generic detail).
DomainKind? findDomainKind(String kind) {
  for (final section in domainSections) {
    for (final k in section.kinds) {
      if (k.kind == kind) return k;
    }
  }
  return null;
}
