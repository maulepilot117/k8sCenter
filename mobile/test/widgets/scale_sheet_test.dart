// Verifies ScaleSheet's contract:
//   - showScaleSheet returns the entered integer on submit
//   - empty input shows inline error and does NOT pop
//   - Cancel pops with null
//   - field is pre-filled with currentReplicas

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/scale_sheet.dart';

/// Wraps a trigger button that captures the future result of showScaleSheet.
Widget _host({
  required String name,
  required int currentReplicas,
  void Function(int?)? onResult,
}) {
  return MaterialApp(
    theme: buildKubeTheme('nexus'),
    home: Scaffold(
      body: Builder(builder: (ctx) {
        return Center(
          child: ElevatedButton(
            onPressed: () async {
              final result = await showScaleSheet(
                context: ctx,
                name: name,
                currentReplicas: currentReplicas,
              );
              onResult?.call(result);
            },
            child: const Text('Open'),
          ),
        );
      }),
    ),
  );
}

void main() {
  testWidgets('submit with valid integer pops the sheet and returns the value',
      (tester) async {
    int? returned;
    await tester.pumpWidget(_host(
      name: 'web',
      currentReplicas: 3,
      onResult: (v) => returned = v,
    ));

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    // Overwrite the pre-filled "3" with "5".
    await tester.enterText(find.byType(TextField), '5');
    await tester.pump();

    await tester.tap(find.widgetWithText(FilledButton, 'Scale'));
    await tester.pumpAndSettle();

    expect(returned, 5);
    // Sheet should be gone.
    expect(find.byType(ScaleSheet), findsNothing);
  });

  testWidgets('empty input shows inline error and does NOT dismiss the sheet',
      (tester) async {
    await tester.pumpWidget(_host(name: 'api', currentReplicas: 2));

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    // Clear the pre-filled value.
    await tester.enterText(find.byType(TextField), '');
    await tester.pump();

    await tester.tap(find.widgetWithText(FilledButton, 'Scale'));
    await tester.pump();

    // Error text rendered inside the InputDecoration.
    expect(find.text('Enter a whole number'), findsOneWidget);
    // Sheet is still open.
    expect(find.byType(ScaleSheet), findsOneWidget);
  });

  testWidgets('Cancel pops with null', (tester) async {
    int? returned = -1; // sentinel to distinguish "never called" from null
    bool called = false;
    await tester.pumpWidget(_host(
      name: 'db',
      currentReplicas: 1,
      onResult: (v) {
        returned = v;
        called = true;
      },
    ));

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();

    expect(called, isTrue);
    expect(returned, isNull);
    expect(find.byType(ScaleSheet), findsNothing);
  });

  testWidgets('field is pre-filled with currentReplicas', (tester) async {
    await tester.pumpWidget(_host(name: 'worker', currentReplicas: 7));

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    final field = tester.widget<TextField>(find.byType(TextField));
    expect(field.controller?.text, '7');
  });

  testWidgets('entering "3" and submitting returns 3', (tester) async {
    int? returned;
    await tester.pumpWidget(_host(
      name: 'cache',
      currentReplicas: 1,
      onResult: (v) => returned = v,
    ));

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField), '3');
    await tester.pump();

    await tester.tap(find.widgetWithText(FilledButton, 'Scale'));
    await tester.pumpAndSettle();

    expect(returned, 3);
  });

  testWidgets('zero replicas is accepted (scale-to-zero)', (tester) async {
    int? returned;
    await tester.pumpWidget(_host(
      name: 'svc',
      currentReplicas: 2,
      onResult: (v) => returned = v,
    ));

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField), '0');
    await tester.pump();

    await tester.tap(find.widgetWithText(FilledButton, 'Scale'));
    await tester.pumpAndSettle();

    expect(returned, 0);
  });
}
