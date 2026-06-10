// Verifies ActionSheet visibility under different RBAC levels:
//   - admin (wildcard): every action for the kind shows
//   - read-only RBAC: empty-state rendered
//   - update-only RBAC: scale/restart/rollback show, delete hidden

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/action_sheet.dart';

final _resource = {
  'kind': 'Deployment',
  'metadata': {'name': 'app', 'namespace': 'default'},
  'spec': {'replicas': 3},
};

Widget _host(RBACSummary rbac) {
  return MaterialApp(
    theme: buildKubeTheme('liquid-glass'),
    home: Scaffold(
      body: Builder(builder: (ctx) {
        return Center(
          child: ElevatedButton(
            onPressed: () => showActionSheet(
              context: ctx,
              kind: 'deployments',
              namespace: 'default',
              resource: _resource,
              rbac: rbac,
            ),
            child: const Text('Open'),
          ),
        );
      }),
    ),
  );
}

void main() {
  testWidgets('admin RBAC renders every action for the kind', (tester) async {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'default': {
          'deployments': ['*'],
        },
      },
    });
    await tester.pumpWidget(_host(rbac));
    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    expect(find.text('Scale'), findsOneWidget);
    expect(find.text('Restart'), findsOneWidget);
    expect(find.text('Rollback'), findsOneWidget);
    expect(find.text('Delete'), findsOneWidget);
  });

  testWidgets('read-only RBAC renders the empty-state', (tester) async {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'default': {
          'deployments': ['get', 'list'],
        },
      },
    });
    await tester.pumpWidget(_host(rbac));
    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    expect(find.text('No actions available for this resource'),
        findsOneWidget);
    expect(find.text('Scale'), findsNothing);
    expect(find.text('Delete'), findsNothing);
  });

  testWidgets('update-only RBAC shows update verbs, hides delete',
      (tester) async {
    final rbac = RBACSummary.fromJson({
      'namespaces': {
        'default': {
          'deployments': ['get', 'list', 'update'],
        },
      },
    });
    await tester.pumpWidget(_host(rbac));
    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    expect(find.text('Scale'), findsOneWidget);
    expect(find.text('Restart'), findsOneWidget);
    expect(find.text('Delete'), findsNothing);
  });

  testWidgets('tap returns the chosen ActionId via Navigator.pop',
      (tester) async {
    String? popped;
    await tester.pumpWidget(MaterialApp(
      theme: buildKubeTheme('liquid-glass'),
      home: Scaffold(
        body: Builder(builder: (ctx) {
          return Center(
            child: ElevatedButton(
              onPressed: () async {
                final id = await showActionSheet(
                  context: ctx,
                  kind: 'deployments',
                  namespace: 'default',
                  resource: _resource,
                  rbac: null,
                );
                popped = id?.name;
              },
              child: const Text('Open'),
            ),
          );
        }),
      ),
    ));
    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Scale'));
    await tester.pumpAndSettle();
    expect(popped, 'scale');
  });
}
