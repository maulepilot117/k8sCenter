// Tests for the per-kind MetricPanel registry — coverage parity with
// the backend monitoring Registry and basic invariants.
//
// F#4 (security audit 2026-05-22) — panels now reference server-side
// slugs instead of carrying raw PromQL templates. PromQL injection
// guards moved to the backend (which validates `namespace` / `name`
// against k8s name rules before substituting into the template); these
// tests cover the slug shape contract that mobile + backend must agree
// on.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/observability/metrics/metric_panels.dart';

void main() {
  group('metricPanelsByKind coverage', () {
    test('every R1 kind has at least one panel', () {
      const requiredKinds = [
        'pods',
        'deployments',
        'statefulsets',
        'daemonsets',
        'nodes',
        'persistentvolumeclaims',
      ];
      for (final k in requiredKinds) {
        expect(metricPanelsByKind[k], isNotNull,
            reason: 'kind $k must have at least one MetricPanel');
        expect(metricPanelsByKind[k]!, isNotEmpty,
            reason: 'kind $k must have at least one MetricPanel');
      }
    });

    test('metricsAvailableForKind returns true for supported kinds', () {
      expect(metricsAvailableForKind('pods'), isTrue);
      expect(metricsAvailableForKind('nodes'), isTrue);
    });

    test('metricsAvailableForKind returns false for unsupported kinds', () {
      expect(metricsAvailableForKind('configmaps'), isFalse);
      expect(metricsAvailableForKind('ingresses'), isFalse);
    });

    test('every panel id is unique within its kind', () {
      for (final entry in metricPanelsByKind.entries) {
        final ids = entry.value.map((p) => p.id).toList();
        final unique = ids.toSet();
        expect(unique.length, ids.length,
            reason: 'duplicate panel id under ${entry.key}: $ids');
      }
    });
  });

  group('MetricPanel slug contract', () {
    test('every panel references a slug in the expected backend namespace',
        () {
      // Slug namespaces must match the backend Registry's grouping. The
      // PVC kind is intentionally `pvcs/*` on the backend even though
      // the K8s plural in the mobile kind map is `persistentvolumeclaims`.
      const expectedNamespace = {
        'pods': 'pods/',
        'deployments': 'deployments/',
        'statefulsets': 'statefulsets/',
        'daemonsets': 'daemonsets/',
        'nodes': 'nodes/',
        'persistentvolumeclaims': 'pvcs/',
      };
      for (final entry in metricPanelsByKind.entries) {
        final prefix = expectedNamespace[entry.key];
        expect(prefix, isNotNull,
            reason: 'mobile kind ${entry.key} has no expected slug prefix '
                'wired into this test — add an entry to expectedNamespace');
        for (final panel in entry.value) {
          expect(panel.slug, startsWith(prefix!),
              reason: 'panel ${panel.id} under ${entry.key} references '
                  'slug ${panel.slug}; expected prefix $prefix');
        }
      }
    });

    test('slugs are kebab-cased ASCII', () {
      // The backend Registry keys are kebab-case ASCII; anything else
      // would either fail to match or hit URL-encoding ambiguity in the
      // slug repo wrapper. This catches accidental camelCase or
      // unicode characters in new panel entries.
      final slugRegex = RegExp(r'^[a-z][a-z0-9\-/]*[a-z0-9]$');
      for (final entry in metricPanelsByKind.entries) {
        for (final panel in entry.value) {
          expect(slugRegex.hasMatch(panel.slug), isTrue,
              reason: 'panel ${panel.id} slug ${panel.slug} is not '
                  'kebab-case ASCII');
        }
      }
    });
  });
}
