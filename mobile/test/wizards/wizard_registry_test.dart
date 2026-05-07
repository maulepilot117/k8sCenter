// Tests for visibleWizards RBAC filtering. The registry is the
// canonical source of which wizards mobile knows about; the filter
// must hide entries the operator's RBAC doesn't permit.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/wizards/wizard_registry.dart';

void main() {
  group('visibleWizards', () {
    test('null RBAC (not yet loaded) returns all entries', () {
      final entries = visibleWizards(rbac: null, namespace: '');
      expect(entries.length, wizardRegistry.length);
    });

    test('cluster-scoped create on configmaps grants ConfigMap entry', () {
      const rbac = RBACSummary(raw: {
        'clusterScoped': {
          'configmaps': ['create', 'get', 'list'],
        },
        'namespaces': <String, dynamic>{},
      });
      final entries = visibleWizards(rbac: rbac, namespace: 'default');
      final cmEntry =
          entries.where((e) => e.type == 'configmap').toList();
      expect(cmEntry, isNotEmpty);
    });

    test('per-namespace create grants entry for that namespace', () {
      const rbac = RBACSummary(raw: {
        'clusterScoped': <String, dynamic>{},
        'namespaces': {
          'default': {
            'configmaps': ['create'],
          },
        },
      });
      final entries = visibleWizards(rbac: rbac, namespace: 'default');
      expect(entries.where((e) => e.type == 'configmap'), isNotEmpty);
    });

    test('per-namespace create in unrelated ns hides entry when active '
        'namespace lacks permission AND fallback says match-any', () {
      // Empty active namespace + allowAnyNamespaceFallback (default for
      // namespaced wizards) lets a per-namespace permission grant the
      // entry. The drawer uses empty ns intentionally so the menu is
      // reachable before the operator picks a namespace.
      const rbac = RBACSummary(raw: {
        'clusterScoped': <String, dynamic>{},
        'namespaces': {
          'team-a': {
            'configmaps': ['create'],
          },
        },
      });
      // With empty namespace and fallback enabled, the entry shows up.
      final viaFallback = visibleWizards(rbac: rbac, namespace: '');
      expect(
          viaFallback.where((e) => e.type == 'configmap'), isNotEmpty);

      // With explicit namespace where the operator has no perm, hidden.
      final scoped = visibleWizards(rbac: rbac, namespace: 'team-b');
      expect(scoped.where((e) => e.type == 'configmap'), isEmpty);
    });

    test('zero RBAC entries hides every wizard', () {
      const rbac = RBACSummary(raw: {
        'clusterScoped': <String, dynamic>{},
        'namespaces': <String, dynamic>{},
      });
      final entries = visibleWizards(rbac: rbac, namespace: 'default');
      expect(entries, isEmpty);
    });

    test('groupByCategory preserves registry order within a group', () {
      final byGroup =
          groupByCategory(visibleWizards(rbac: null, namespace: ''));
      // Configuration group should hold ConfigMap before Secret (the
      // registry order). Order matters: the drawer renders in this
      // exact sequence.
      final config = byGroup['Configuration'];
      expect(config, isNotNull);
      expect(config!.first.type, 'configmap');
      expect(config[1].type, 'secret');
    });

    test('findWizardEntry returns the entry by type', () {
      expect(findWizardEntry('configmap')?.label, 'ConfigMap');
      expect(findWizardEntry('nope'), isNull);
    });
  });
}
