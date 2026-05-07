// Verifies kindDetailPath builds correct routes for each kind in
// domainSections plus the generic-detail fallback.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/routing/domain_sections.dart';

void main() {
  group('kindDetailPath', () {
    test('namespaced kind interpolates clusterId + section + namespace + name',
        () {
      expect(
        kindDetailPath(
          clusterId: 'prod',
          kind: 'pods',
          namespace: 'default',
          name: 'nginx-7d4f-abc12',
        ),
        '/clusters/prod/workloads/pods/default/nginx-7d4f-abc12',
      );
    });

    test('cluster-scoped kind omits namespace segment', () {
      expect(
        kindDetailPath(
          clusterId: 'local',
          kind: 'nodes',
          namespace: '',
          name: 'node-1',
        ),
        '/clusters/local/cluster/nodes/node-1',
      );
    });

    test('config section uses pathSegment (config) not label (Configuration)',
        () {
      expect(
        kindDetailPath(
          clusterId: 'local',
          kind: 'configmaps',
          namespace: 'kube-system',
          name: 'coredns',
        ),
        '/clusters/local/config/configmaps/kube-system/coredns',
      );
    });

    test('unknown kind falls through to generic-detail catch-all', () {
      expect(
        kindDetailPath(
          clusterId: 'local',
          kind: 'networkpolicies',
          namespace: 'default',
          name: 'allow-egress',
        ),
        '/clusters/local/generic/networkpolicies/default/allow-egress',
      );
    });

    test('unknown cluster-scoped kind uses sentinel namespace', () {
      expect(
        kindDetailPath(
          clusterId: 'local',
          kind: 'customresources',
          namespace: '',
          name: 'cr-1',
        ),
        '/clusters/local/generic/customresources/_/cr-1',
      );
      expect(clusterScopedNamespaceSentinel, '_');
    });

    test('namespace literally named "cluster" is preserved (no collision)', () {
      // Pre-fix the route used 'cluster' as the cluster-scoped sentinel,
      // which would mis-route a real namespace named 'cluster'. Sentinel
      // is now '_' (illegal in DNS-1123) so 'cluster' passes through.
      expect(
        kindDetailPath(
          clusterId: 'local',
          kind: 'pods',
          namespace: 'cluster',
          name: 'p1',
        ),
        '/clusters/local/workloads/pods/cluster/p1',
      );
    });
  });

  group('findDomainSection', () {
    test('returns the owning section for a known kind', () {
      expect(findDomainSection('pods')?.label, 'Workloads');
      expect(findDomainSection('services')?.label, 'Networking');
      expect(findDomainSection('configmaps')?.label, 'Configuration');
      expect(findDomainSection('nodes')?.label, 'Cluster');
    });
    test('returns null for unknown kind', () {
      expect(findDomainSection('not-a-kind'), isNull);
    });
  });
}
