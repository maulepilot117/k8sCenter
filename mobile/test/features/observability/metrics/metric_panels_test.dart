// Tests for the per-kind MetricPanel registry — variable substitution,
// injection guards, and coverage for the six R1-required kinds.

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

  group('MetricPanel.render', () {
    test('substitutes declared variables', () {
      final panel = metricPanelsByKind['pods']!
          .firstWhere((p) => p.id == 'pod_cpu_usage');
      final rendered = panel.render({
        'namespace': 'default',
        'pod': 'web-abc',
      });
      expect(rendered, contains('namespace="default"'));
      expect(rendered, contains('pod="web-abc"'));
      // No leftover variables.
      expect(rendered, isNot(contains(r'$pod')));
      expect(rendered, isNot(contains(r'$namespace')));
    });

    test('throws ArgumentError on missing variable', () {
      final panel = metricPanelsByKind['pods']!.first;
      expect(
        () => panel.render(const {'namespace': 'default'}),
        throwsArgumentError,
      );
    });

    test('rejects values that would break the Kubernetes name regex '
        '(PromQL injection defense)', () {
      final panel = metricPanelsByKind['pods']!.first;
      // The backend's `isValidK8sName` rejects values with quotes,
      // braces, or whitespace — these are the exact characters an
      // attacker would use to break out of the PromQL label-value
      // string. The client-side guard catches them before they hit the
      // wire.
      expect(
        () => panel.render({
          'namespace': 'default',
          'pod': 'evil"} ; drop_metrics()',
        }),
        throwsArgumentError,
      );
      expect(
        () => panel.render({
          'namespace': 'default',
          'pod': '', // empty value
        }),
        throwsArgumentError,
      );
    });
  });
}
