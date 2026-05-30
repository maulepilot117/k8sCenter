// Helper coverage: K8sMeta extraction, age formatting, label join,
// nested path read.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/resources/k8s_helpers.dart';

void main() {
  group('K8sMeta.from', () {
    test('extracts name + namespace + labels', () {
      final m = K8sMeta.from({
        'metadata': {
          'name': 'my-pod',
          'namespace': 'default',
          'uid': 'abc-123',
          'creationTimestamp': '2026-05-01T00:00:00Z',
          'labels': {'app': 'web', 'version': 'v1'},
          'annotations': {'note': 'hi'},
        },
      });
      expect(m.name, 'my-pod');
      expect(m.namespace, 'default');
      expect(m.uid, 'abc-123');
      expect(m.labels['app'], 'web');
      expect(m.annotations['note'], 'hi');
    });

    test('handles missing metadata', () {
      final m = K8sMeta.from(<String, dynamic>{});
      expect(m.name, '');
      expect(m.namespace, '');
      expect(m.labels, isEmpty);
    });
  });

  group('formatAge', () {
    test('returns "—" for empty timestamp', () {
      expect(formatAge(''), '—');
    });
    test('returns "—" for invalid format', () {
      expect(formatAge('not-a-date'), '—');
    });
    test('formats recent timestamp as seconds/minutes/hours', () {
      final now = DateTime.now().toUtc();
      final fiveMinAgo = now.subtract(const Duration(minutes: 5));
      expect(formatAge(fiveMinAgo.toIso8601String()), '5m');
      final twoHoursAgo = now.subtract(const Duration(hours: 2));
      expect(formatAge(twoHoursAgo.toIso8601String()), '2h');
    });
    test('formats older timestamps as days/months/years', () {
      final now = DateTime.now().toUtc();
      expect(
        formatAge(now.subtract(const Duration(days: 5)).toIso8601String()),
        '5d',
      );
      expect(
        formatAge(now.subtract(const Duration(days: 60)).toIso8601String()),
        '2mo',
      );
      expect(
        formatAge(now.subtract(const Duration(days: 800)).toIso8601String()),
        '2y',
      );
    });
    test('clamps future/clock-skewed timestamps to "0s"', () {
      final future =
          DateTime.now().toUtc().add(const Duration(seconds: 5));
      expect(formatAge(future.toIso8601String()), '0s');
      final now = DateTime.now().toUtc();
      expect(formatAge(now.toIso8601String()), '0s');
    });
  });

  group('joinMap', () {
    test('returns "—" for empty map', () {
      expect(joinMap(const {}), '—');
    });
    test('joins entries with comma', () {
      expect(joinMap({'a': '1', 'b': '2'}), 'a=1, b=2');
    });
    test('truncates with "+N more" past maxEntries', () {
      final m = {for (var i = 0; i < 8; i++) 'k$i': '$i'};
      final joined = joinMap(m, maxEntries: 3);
      expect(joined, contains('+5 more'));
    });
  });

  group('readPath', () {
    test('returns nested value', () {
      final r = {
        'spec': {'replicas': 3, 'strategy': {'type': 'RollingUpdate'}},
      };
      expect(readPath(r, 'spec.replicas'), 3);
      expect(readPath(r, 'spec.strategy.type'), 'RollingUpdate');
    });
    test('returns null for missing segment', () {
      expect(
        readPath(const {'spec': <String, dynamic>{}}, 'spec.replicas'),
        isNull,
      );
      expect(readPath(const <String, dynamic>{}, 'spec.replicas'), isNull);
    });
    test('handles non-Map intermediate (array)', () {
      final r = {'spec': {'containers': [1, 2, 3]}};
      expect(readPath(r, 'spec.containers.0'), isNull);
    });
  });
}
