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

  test(
      'empty namespace with allowAnyNamespaceFallback allows the verb if '
      'granted in ANY namespace (All-Namespaces list view)', () {
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
    expect(
      canPerform(rbac, 'pods', 'delete', '', allowAnyNamespaceFallback: true),
      isTrue,
    );
    expect(
      canPerform(rbac, 'pods', 'create', '', allowAnyNamespaceFallback: true),
      isFalse,
    );
  });

  test(
      'empty namespace WITHOUT allowAnyNamespaceFallback denies '
      '(detail-view of cluster-scoped resource — only clusterScoped grants)',
      () {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'app1': {
          'pods': ['delete'],
        },
      },
    });
    // Default is allowAnyNamespaceFallback: false. A namespaced grant for
    // 'pods' in 'app1' must NOT bleed into the detail-view check for a
    // cluster-scoped resource (where namespace is empty).
    expect(canPerform(rbac, 'pods', 'delete', ''), isFalse);
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
