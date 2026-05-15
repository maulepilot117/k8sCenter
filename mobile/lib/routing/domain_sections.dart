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
  // Service mesh surfaces (PR-4f). Three entries:
  //   * Mesh dashboard — Istio + Linkerd side-by-side status cards.
  //   * Routing — list of every TrafficRoute across both meshes.
  //   * mTLS posture — per-namespace workload encryption state.
  // Golden signals tab is rendered inline on Service detail screens
  // rather than as a drawer entry. All three surfaces gate on
  // `meshStatusProvider` and fall back to
  // `FeatureUnavailableState.mesh()` when neither mesh is installed.
  DomainSection(
    label: 'Service Mesh',
    pathSegment: 'mesh',
    icon: Icons.hub_outlined,
    kinds: [
      DomainKind(
        kind: 'mesh',
        label: 'Dashboard',
        icon: Icons.dashboard_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/mesh',
      ),
      DomainKind(
        kind: 'routing',
        label: 'Routing',
        icon: Icons.alt_route_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/mesh/routing',
      ),
      DomainKind(
        kind: 'mtls',
        label: 'mTLS posture',
        icon: Icons.lock_outline,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/mesh/mtls',
      ),
    ],
  ),
  // External Secrets Operator observatory (PR-4h). Six entries:
  //   * Dashboard — synced/total gauge + SyncFailed/Stale/Drifted/
  //     Unknown summary cards + top-N failure table.
  //   * ExternalSecrets — namespaced list with `lastObservedDriftStatus`
  //     hint per row; detail screen fetches live `driftStatus`.
  //   * Cluster ExternalSecrets — fan-out form, cluster-scoped.
  //   * SecretStores — namespaced + per-store metrics panel (rate +
  //     cost-tier estimate).
  //   * Cluster SecretStores — cluster-scoped store variant.
  //   * PushSecrets — read-only inverse direction (Secret → store).
  // All six surfaces gate on `esoStatusProvider` and fall back to
  // `FeatureUnavailableState.eso()` when not installed. Drift Revert +
  // bulk-refresh write actions are deferred to M5+ per R12.
  DomainSection(
    label: 'External Secrets',
    pathSegment: 'eso',
    icon: Icons.lock_person_outlined,
    kinds: [
      DomainKind(
        kind: 'eso-dashboard',
        label: 'Dashboard',
        icon: Icons.dashboard_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/eso',
      ),
      DomainKind(
        kind: 'externalsecrets',
        label: 'ExternalSecrets',
        icon: Icons.lock_open_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/eso/externalsecrets',
      ),
      DomainKind(
        kind: 'cluster-externalsecrets',
        label: 'Cluster ExternalSecrets',
        icon: Icons.public_outlined,
        namespaced: false,
        customListPath:
            '/clusters/{clusterId}/eso/cluster-externalsecrets',
      ),
      DomainKind(
        kind: 'eso-stores',
        label: 'SecretStores',
        icon: Icons.account_tree_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/eso/stores',
      ),
      DomainKind(
        kind: 'eso-cluster-stores',
        label: 'Cluster SecretStores',
        icon: Icons.public_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/eso/cluster-stores',
      ),
      DomainKind(
        kind: 'pushsecrets',
        label: 'PushSecrets',
        icon: Icons.upload_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/eso/pushsecrets',
      ),
    ],
  ),
  // Policy compliance browser (PR-4i). Three entries:
  //   * Dashboard — compliance score (KubeGaugeRing) + by-engine cards
  //     + by-severity breakdown + browse tiles.
  //   * Policies — full list with engine / severity / blocking chips +
  //     search. Engine-availability badges (per PR-3f intersection
  //     learning) on rows whose engine isn't installed.
  //   * Violations — RBAC-filtered violations list with namespace +
  //     severity filters; virtual scroll for 1000+-violation responses.
  // All three surfaces gate on `policyStatusProvider` and fall back to
  // `FeatureUnavailableState.policy()` when neither engine is detected.
  // Compliance history (admin-only) is reachable from the dashboard
  // browse tiles rather than as a top-level drawer entry — it's a niche
  // workflow that doesn't deserve permanent drawer real estate.
  DomainSection(
    label: 'Policy',
    pathSegment: 'policy',
    icon: Icons.policy_outlined,
    kinds: [
      DomainKind(
        kind: 'policy-dashboard',
        label: 'Dashboard',
        icon: Icons.dashboard_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/policy',
      ),
      DomainKind(
        kind: 'policies',
        label: 'Policies',
        icon: Icons.list_alt_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/policy/policies',
      ),
      DomainKind(
        kind: 'violations',
        label: 'Violations',
        icon: Icons.report_problem_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/policy/violations',
      ),
    ],
  ),
  // Cert-manager observatory (PR-4g). Three entries:
  //   * Certificates — full list with filter chips (All / Expiring /
  //     Failed) + search. `?status=expiring` URL param pre-filters.
  //   * Issuers — combined namespaced + cluster Issuers.
  //   * Expiring — backend-sorted summary list of certs nearing expiry.
  // All three surfaces gate on `certManagerStatusProvider` and fall
  // back to `FeatureUnavailableState.certManager()` when not installed.
  DomainSection(
    label: 'Certificates',
    pathSegment: 'certificates',
    icon: Icons.verified_outlined,
    kinds: [
      DomainKind(
        kind: 'certificates',
        label: 'Certificates',
        icon: Icons.verified_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/certificates/certificates',
      ),
      DomainKind(
        kind: 'issuers',
        label: 'Issuers',
        icon: Icons.assignment_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/certificates/issuers',
      ),
      DomainKind(
        kind: 'expiring',
        label: 'Expiring',
        icon: Icons.timer_outlined,
        namespaced: false,
        customListPath: '/clusters/{clusterId}/certificates/expiring',
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
