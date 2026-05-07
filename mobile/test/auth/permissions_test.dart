// canPerform RBAC predicate. Port of frontend/lib/permissions.ts tests.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/permissions.dart';
import 'package:kubecenter/auth/user.dart';

void main() {
  test('null rbac returns true (optimistic until loaded)', () {
    expect(canPerform(null, 'pods', 'delete', 'default'), isTrue);
  });

  test('clusterScoped grant applies regardless of namespace', () {
    final rbac = RBACSummary.fromJson({
      'clusterScoped': {
        'nodes': ['get', 'list'],
      },
    });
    expect(canPerform(rbac, 'nodes', 'get', ''), isTrue);
    expect(canPerform(rbac, 'nodes', 'get', 'any-ns'), isTrue);
  });

  test('namespaced grant only applies in that namespace', () {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'app': {
          'deployments': ['get', 'update'],
        },
      },
    });
    expect(canPerform(rbac, 'deployments', 'update', 'app'), isTrue);
    expect(canPerform(rbac, 'deployments', 'update', 'other'), isFalse);
  });

  test('wildcard verb (*) allows any verb', () {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'app': {
          'deployments': ['*'],
        },
      },
    });
    expect(canPerform(rbac, 'deployments', 'delete', 'app'), isTrue);
    expect(canPerform(rbac, 'deployments', 'patch', 'app'), isTrue);
  });

  test('empty namespace allows the verb if granted in ANY namespace', () {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'app1': {
          'pods': ['delete'],
        },
        'app2': {
          'pods': ['get'],
        },
      },
    });
    expect(canPerform(rbac, 'pods', 'delete', ''), isTrue);
    expect(canPerform(rbac, 'pods', 'create', ''), isFalse);
  });

  test('missing kind in namespace returns false', () {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'app': {
          'deployments': ['*'],
        },
      },
    });
    expect(canPerform(rbac, 'pods', 'get', 'app'), isFalse);
  });

  test('verb not in list returns false', () {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'app': {
          'deployments': ['get', 'list'],
        },
      },
    });
    expect(canPerform(rbac, 'deployments', 'delete', 'app'), isFalse);
  });
}
