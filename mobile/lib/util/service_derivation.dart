// PR-5f service-name autoderivation. Given a Pod or Deployment-like
// resource's labels, finds Services in the same namespace whose
// `spec.selector` is a non-empty subset of those labels. Returns
// matches sorted by selector specificity (most-specific first) with
// alphabetical tie-break.
//
// Used by Pod and Deployment detail screens to decide whether to
// surface the Golden Signals tab — and, when more than one Service
// matches, to feed the in-tab picker.
//
// Two surface layers:
//   - The pure function `findServicesForResource` is the algorithm.
//   - The Riverpod `derivedServicesProvider` family caches the result
//     keyed on (clusterId, namespace, resourceLabels) so the O(services
//     × selector_keys) work only re-runs when its inputs actually
//     change, not on every detail-screen rebuild.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/resource_repository.dart';

/// A Service-name match for the Golden Signals derivation. Carries
/// [selectorSize] so consumers (the tab picker, sort-stability tests)
/// can reason about why one match is ranked above another without
/// re-deriving from raw unstructured maps.
class DerivedService {
  const DerivedService({
    required this.name,
    required this.namespace,
    required this.selectorSize,
  });

  final String name;
  final String namespace;

  /// Number of label keys in the matching Service's `spec.selector`.
  /// Larger = more specific (the Service narrows pods more tightly);
  /// the matches list is sorted by this descending.
  final int selectorSize;

  @override
  bool operator ==(Object other) =>
      other is DerivedService &&
      other.name == name &&
      other.namespace == namespace &&
      other.selectorSize == selectorSize;

  @override
  int get hashCode => Object.hash(name, namespace, selectorSize);

  @override
  String toString() =>
      'DerivedService($namespace/$name, selectorSize=$selectorSize)';
}

/// Walks [services] (raw unstructured Service resources from the
/// `resourceListProvider`) and returns every Service in [namespace]
/// whose `spec.selector` is a non-empty subset of [resourceLabels].
///
/// Empty selectors are *not* treated as matches even though they would
/// technically target every pod — a Service with an empty selector is
/// almost always a headless endpoint or an externally-fronted ExternalName,
/// neither of which should pollute the Golden Signals tab.
///
/// Sort: most-specific selector first (larger selector = more pods it
/// excludes from the match), then alphabetical by Service name for a
/// stable display order in the picker.
List<DerivedService> findServicesForResource({
  required List<Map<String, dynamic>> services,
  required String namespace,
  required Map<String, String> resourceLabels,
}) {
  if (namespace.isEmpty) return const [];
  // No labels → nothing can target this resource via label selector.
  // A Service with selector {app: web} requires the Pod to also carry
  // `app=web`; with empty labels, every selector fails the subset test.
  if (resourceLabels.isEmpty) return const [];

  final matches = <DerivedService>[];
  for (final svc in services) {
    final meta = svc['metadata'];
    if (meta is! Map) continue;
    final svcNamespace = meta['namespace'] as String? ?? '';
    if (svcNamespace != namespace) continue;
    final name = meta['name'] as String?;
    if (name == null || name.isEmpty) continue;

    final spec = svc['spec'];
    if (spec is! Map) continue;
    final selector = spec['selector'];
    if (selector is! Map || selector.isEmpty) continue;

    // Coerce selector to String/String; reject non-string values rather
    // than guessing (Kubernetes label values are strings by spec, but
    // unstructured JSON parsing can leak nulls or numbers through).
    final selectorMap = <String, String>{};
    var malformed = false;
    selector.forEach((k, v) {
      if (k is String && v is String) {
        selectorMap[k] = v;
      } else {
        malformed = true;
      }
    });
    if (malformed) continue;

    var isSubset = true;
    selectorMap.forEach((k, v) {
      if (resourceLabels[k] != v) isSubset = false;
    });
    if (!isSubset) continue;

    matches.add(DerivedService(
      name: name,
      namespace: namespace,
      selectorSize: selectorMap.length,
    ));
  }

  matches.sort((a, b) {
    if (a.selectorSize != b.selectorSize) {
      return b.selectorSize.compareTo(a.selectorSize);
    }
    return a.name.compareTo(b.name);
  });
  return matches;
}

/// Composite key for [derivedServicesProvider]. The labels map is
/// hashed by entry — two Pods with the same `{app: web, tier: web}`
/// share a cache slot regardless of insertion order.
class DerivedServicesKey {
  DerivedServicesKey({
    required this.clusterId,
    required this.namespace,
    required Map<String, String> resourceLabels,
  }) : resourceLabels = Map<String, String>.unmodifiable(resourceLabels);

  final String clusterId;
  final String namespace;
  final Map<String, String> resourceLabels;

  @override
  bool operator ==(Object other) {
    if (other is! DerivedServicesKey) return false;
    if (other.clusterId != clusterId) return false;
    if (other.namespace != namespace) return false;
    if (other.resourceLabels.length != resourceLabels.length) return false;
    for (final entry in resourceLabels.entries) {
      if (other.resourceLabels[entry.key] != entry.value) return false;
    }
    return true;
  }

  @override
  int get hashCode {
    var labelHash = 0;
    for (final entry in resourceLabels.entries) {
      // XOR per-entry hashes so map ordering doesn't affect the result.
      labelHash ^= Object.hash(entry.key, entry.value);
    }
    return Object.hash(clusterId, namespace, labelHash);
  }
}

/// Watches the namespace's Service list and runs [findServicesForResource]
/// against the current labels. Returns the empty list while the Service
/// list is loading or errored so consumers can use `.isNotEmpty` as the
/// gate for "show Golden Signals tab". The provider recomputes only
/// when the upstream Service list or any key field changes — the same
/// detail-screen rebuilding repeatedly with identical inputs hits the
/// Riverpod cache, not the O(services × selector_keys) loop.
final derivedServicesProvider = Provider.autoDispose
    .family<List<DerivedService>, DerivedServicesKey>((ref, key) {
  final servicesAsync = ref.watch(resourceListProvider(ResourceListKey(
    clusterId: key.clusterId,
    kind: 'services',
    namespace: key.namespace,
  )));
  final items =
      servicesAsync.valueOrNull?.items ?? const <Map<String, dynamic>>[];
  return findServicesForResource(
    services: items,
    namespace: key.namespace,
    resourceLabels: key.resourceLabels,
  );
});
