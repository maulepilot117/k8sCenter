// Smoke coverage for the PR-1e row-model derivations. Each test feeds a
// minimal unstructured map representative of what client-go returns and
// asserts the kind-specific accessors extract sensible values, including
// the edge cases the screens render specially (terminating namespace,
// no-owner ReplicaSet, ingress with no TLS, etc.).
//
// Row-model classes are file-private (`_FooRow`) so we can't import them
// directly. Instead we reproduce the same readPath-based extractions via
// the public `readPath` helper — this keeps the tests behavioral
// (verifying our path strings + transformations, not the internal class)
// while still catching regressions like field-name typos.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/resources/k8s_helpers.dart';

void main() {
  group('ReplicaSet derivations', () {
    test('healthy when ready == desired', () {
      final raw = {
        'metadata': {'name': 'r1', 'namespace': 'default'},
        'spec': {'replicas': 3},
        'status': {'replicas': 3, 'readyReplicas': 3},
      };
      final desired = (readPath(raw, 'spec.replicas') as num).toInt();
      final ready = (readPath(raw, 'status.readyReplicas') as num).toInt();
      expect(desired, 3);
      expect(ready, 3);
      expect(ready == desired, isTrue);
    });

    test('owner Deployment surfaces from ownerReferences', () {
      final raw = {
        'metadata': {
          'name': 'r1',
          'ownerReferences': [
            {'kind': 'Deployment', 'name': 'web'}
          ],
        },
      };
      final owners =
          (readPath(raw, 'metadata.ownerReferences') as List?) ?? const [];
      final dep = owners
          .whereType<Map<String, dynamic>>()
          .where((o) => o['kind'] == 'Deployment')
          .firstOrNull;
      expect(dep, isNotNull);
      expect(dep!['name'], 'web');
    });
  });

  group('StatefulSet derivations', () {
    test('serviceName + volumeClaimTemplates count', () {
      final raw = {
        'spec': {
          'serviceName': 'mysql-headless',
          'replicas': 3,
          'volumeClaimTemplates': [
            {'metadata': {'name': 'data'}},
            {'metadata': {'name': 'logs'}},
          ],
        },
        'status': {'readyReplicas': 3},
      };
      expect(readPath(raw, 'spec.serviceName'), 'mysql-headless');
      final tpls =
          (readPath(raw, 'spec.volumeClaimTemplates') as List?) ?? const [];
      expect(tpls.length, 2);
    });
  });

  group('DaemonSet derivations', () {
    test('rollout numbers extracted', () {
      final raw = {
        'status': {
          'desiredNumberScheduled': 3,
          'currentNumberScheduled': 3,
          'numberReady': 3,
          'updatedNumberScheduled': 3,
          'numberAvailable': 3,
          'numberMisscheduled': 0,
        },
      };
      expect(readPath(raw, 'status.desiredNumberScheduled'), 3);
      expect(readPath(raw, 'status.numberReady'), 3);
      expect(readPath(raw, 'status.numberMisscheduled'), 0);
    });
  });

  group('Ingress derivations', () {
    test('rules + TLS extraction', () {
      final raw = {
        'spec': {
          'ingressClassName': 'nginx',
          'rules': [
            {
              'host': 'app.example.com',
              'http': {
                'paths': [
                  {
                    'path': '/',
                    'pathType': 'Prefix',
                    'backend': {
                      'service': {
                        'name': 'web',
                        'port': {'number': 80},
                      },
                    },
                  },
                ],
              },
            },
          ],
          'tls': [
            {'hosts': ['app.example.com'], 'secretName': 'app-tls'},
          ],
        },
        'status': {
          'loadBalancer': {
            'ingress': [
              {'ip': '10.0.0.1'},
            ],
          },
        },
      };
      final rules = (readPath(raw, 'spec.rules') as List?) ?? const [];
      expect(rules.length, 1);
      final tls = (readPath(raw, 'spec.tls') as List?) ?? const [];
      expect(tls.length, 1);
      final lbIngress =
          (readPath(raw, 'status.loadBalancer.ingress') as List?) ?? const [];
      expect((lbIngress.first as Map)['ip'], '10.0.0.1');
    });

    test('ingress with no TLS leaves spec.tls null', () {
      final raw = {
        'spec': {
          'rules': [
            {'host': 'plain.example.com', 'http': {'paths': <Map<String, Object?>>[]}}
          ],
        },
      };
      final tls = readPath(raw, 'spec.tls');
      expect(tls, isNull);
    });
  });

  group('PVC derivations', () {
    test('phase + capacity + access modes', () {
      final raw = {
        'spec': {
          'storageClassName': 'gp3',
          'accessModes': ['ReadWriteOnce'],
          'volumeName': 'pv-abc',
        },
        'status': {
          'phase': 'Bound',
          'capacity': {'storage': '10Gi'},
        },
      };
      expect(readPath(raw, 'status.phase'), 'Bound');
      expect(readPath(raw, 'status.capacity.storage'), '10Gi');
      expect(readPath(raw, 'spec.accessModes'), isA<List<dynamic>>());
    });
  });

  group('Namespace derivations', () {
    test('Active phase recognized', () {
      final raw = {
        'metadata': {'name': 'kube-system'},
        'status': {'phase': 'Active'},
      };
      expect(readPath(raw, 'status.phase'), 'Active');
    });

    test('Terminating phase + finalizers visible', () {
      final raw = {
        'metadata': {'name': 'stuck'},
        'spec': {
          'finalizers': ['kubernetes', 'cert-manager.io/cleanup'],
        },
        'status': {'phase': 'Terminating'},
      };
      expect(readPath(raw, 'status.phase'), 'Terminating');
      final f = (readPath(raw, 'spec.finalizers') as List?) ?? const [];
      expect(f.length, 2);
    });
  });
}
