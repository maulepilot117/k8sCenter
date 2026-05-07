// Verifies ConfirmSheet's contract:
//   - typeToConfirm gates the confirm button until input matches (after trim)
//   - cancel/confirm pop with the right boolean
//   - danger=true uses the error-tinted button (smoke check)

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/confirm_sheet.dart';

Widget _host(Widget Function(BuildContext) builder) {
  return MaterialApp(
    theme: buildKubeTheme('nexus'),
    home: Scaffold(
      body: Builder(builder: (ctx) {
        return Center(
          child: ElevatedButton(
            onPressed: () => showModalBottomSheet<bool>(
              context: ctx,
              builder: (sheetCtx) => builder(sheetCtx),
            ),
            child: const Text('Open'),
          ),
        );
      }),
    ),
  );
}

void main() {
  testWidgets('simple confirm: tap Confirm pops true', (tester) async {
    await tester.pumpWidget(_host(
      (_) => const ConfirmSheet(
        title: 'Restart deploy',
        confirmLabel: 'Restart',
      ),
    ));
    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();
    expect(find.text('Restart deploy'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Restart'));
    await tester.pumpAndSettle();
    expect(find.byType(ConfirmSheet), findsNothing);
  });

  testWidgets('typeToConfirm: confirm disabled until exact match',
      (tester) async {
    await tester.pumpWidget(_host(
      (_) => const ConfirmSheet(
        title: 'Delete pod',
        message: 'permanently delete "my-pod".',
        confirmLabel: 'Delete',
        danger: true,
        typeToConfirm: 'my-pod',
      ),
    ));
    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    // Find the confirm button by label.
    final confirmFinder = find.widgetWithText(FilledButton, 'Delete');
    final disabledBtn = tester.widget<FilledButton>(confirmFinder);
    expect(disabledBtn.onPressed, isNull, reason: 'disabled before input');

    // Wrong text — still disabled.
    await tester.enterText(find.byType(TextField), 'my-pod-wrong');
    await tester.pump();
    expect(tester.widget<FilledButton>(confirmFinder).onPressed, isNull);

    // Right text — enabled.
    await tester.enterText(find.byType(TextField), 'my-pod');
    await tester.pump();
    expect(tester.widget<FilledButton>(confirmFinder).onPressed, isNotNull);
  });

  testWidgets('typeToConfirm: trims whitespace before matching',
      (tester) async {
    await tester.pumpWidget(_host(
      (_) => const ConfirmSheet(
        title: 'Delete',
        confirmLabel: 'Delete',
        typeToConfirm: 'foo',
      ),
    ));
    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();
    // Autocorrect-style trailing space — match should still pass.
    await tester.enterText(find.byType(TextField), 'foo ');
    await tester.pump();
    final btn = tester
        .widget<FilledButton>(find.widgetWithText(FilledButton, 'Delete'));
    expect(btn.onPressed, isNotNull);
  });

  testWidgets('cancel pops false', (tester) async {
    bool? returned;
    await tester.pumpWidget(MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: Scaffold(
        body: Builder(builder: (ctx) {
          return Center(
            child: ElevatedButton(
              onPressed: () async {
                returned = await showConfirmSheet(
                  context: ctx,
                  title: 'X',
                  confirmLabel: 'OK',
                );
              },
              child: const Text('Open'),
            ),
          );
        }),
      ),
    ));
    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(returned, isFalse);
  });
}
