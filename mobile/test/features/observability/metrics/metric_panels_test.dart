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

    test('accepts legitimate names that the backend may still RFC-1123-reject '
        '(uppercase, underscore) — defers strict validation to backend',
        () {
      // Kubelet's --hostname-override historically allowed uppercase and
      // underscore on on-prem nodes. The client-side guard should not
      // dead-end such names; backend returns a 400 with a precise error
      // if the value is actually invalid per RFC 1123.
      final panel = metricPanelsByKind['nodes']!.first;
      expect(
        () => panel.render({'node': 'WORKER_01.corp.local'}),
        returnsNormally,
      );
    });

    test('rejects values containing PromQL-breaking characters', () {
      final panel = metricPanelsByKind['pods']!.first;
      // Double-quote closes the label-value string — direct injection.
      expect(
        () => panel.render({'namespace': 'default', 'pod': 'evil"; up'}),
        throwsArgumentError,
      );
      // Backslash escapes the next char — escape-chain injection.
      expect(
        () => panel.render({'namespace': 'default', 'pod': 'a\\b'}),
        throwsArgumentError,
      );
      // Dollar — defence-in-depth against double-substitution.
      expect(
        () => panel.render({'namespace': 'default', 'pod': 'a\$b'}),
        throwsArgumentError,
      );
      // Newline — breaks the wire format.
      expect(
        () => panel.render({'namespace': 'default', 'pod': 'a\nb'}),
        throwsArgumentError,
      );
      // Empty — still rejected (no substitution target).
      expect(
        () => panel.render({'namespace': 'default', 'pod': ''}),
        throwsArgumentError,
      );
      // 254-char name — over length cap.
      expect(
        () => panel.render({
          'namespace': 'default',
          'pod': 'a' * 254,
        }),
        throwsArgumentError,
      );
    });

    test('accepts boundary-valid 253-char name', () {
      final panel = metricPanelsByKind['pods']!.first;
      expect(
        () => panel.render({
          'namespace': 'default',
          'pod': 'a' * 253,
        }),
        returnsNormally,
      );
    });
  });
}
