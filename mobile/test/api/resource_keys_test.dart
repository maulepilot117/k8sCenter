// Riverpod family keys MUST honor equality + hashCode for cache reuse.
// Without clusterId in the key, switching clusters would reuse cached
// entries from the prior cluster — wrong-cluster data shown silently.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/resource_repository.dart';

void main() {
  group('ResourceListKey', () {
    test('equal keys with same fields hash equal', () {
      const a = ResourceListKey(
        clusterId: 'local',
        kind: 'pods',
        namespace: 'default',
      );
      const b = ResourceListKey(
        clusterId: 'local',
        kind: 'pods',
        namespace: 'default',
      );
      expect(a, equals(b));
      expect(a.hashCode, b.hashCode);
    });

    test('different clusterId produces different key (no cache bleed)', () {
      const a = ResourceListKey(clusterId: 'local', kind: 'pods');
      const b = ResourceListKey(clusterId: 'prod', kind: 'pods');
      expect(a, isNot(equals(b)));
    });

    test('different kind / namespace / labelSelector each produces a new key',
        () {
      const base = ResourceListKey(
        clusterId: 'local',
        kind: 'pods',
        namespace: 'default',
        labelSelector: 'app=web',
      );
      expect(
        base,
        isNot(equals(const ResourceListKey(
          clusterId: 'local',
          kind: 'deployments',
          namespace: 'default',
          labelSelector: 'app=web',
        ))),
      );
      expect(
        base,
        isNot(equals(const ResourceListKey(
          clusterId: 'local',
          kind: 'pods',
          namespace: 'kube-system',
          labelSelector: 'app=web',
        ))),
      );
      expect(
        base,
        isNot(equals(const ResourceListKey(
          clusterId: 'local',
          kind: 'pods',
          namespace: 'default',
          labelSelector: 'app=api',
        ))),
      );
    });
  });

  group('ResourceGetKey', () {
    test('clusterId distinguishes otherwise-identical keys', () {
      const a = ResourceGetKey(
        clusterId: 'local',
        kind: 'pods',
        namespace: 'default',
        name: 'p1',
      );
      const b = ResourceGetKey(
        clusterId: 'prod',
        kind: 'pods',
        namespace: 'default',
        name: 'p1',
      );
      expect(a, isNot(equals(b)));
      expect(a.hashCode, isNot(b.hashCode));
    });
  });
}
